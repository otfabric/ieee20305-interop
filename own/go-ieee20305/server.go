// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/csipaus/csipserver"
	"github.com/otfabric/go-ieee20305/security"
	"github.com/otfabric/go-ieee20305/server"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// seededDevice is a provisioned device and the hrefs of what it was seeded
// with.
type seededDevice struct {
	LFDI      security.LFDI
	SFDI      string
	EndDevice string
	FSA       string
	Programs  map[string]string // description -> href
	ReplyTo   string
}

func runServer(args []string) error {
	fs, err := parseFlags(args, "version", "port", "tls-dir", "fixture", "ready-file", "control-socket")
	if err != nil {
		return err
	}
	version, err := fs.edition()
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(fs.get("port", "443"))
	if err != nil || port < 1 || port > 65535 {
		return usagef("--port %q is not a TCP port", fs.get("port", ""))
	}
	tlsDir, err := fs.required("tls-dir")
	if err != nil {
		return err
	}
	f, err := loadFixture(fs.get("fixture", defaultFixture))
	if err != nil {
		return err
	}
	edition, err := ieee20305.ParseEdition(version)
	if err != nil {
		return usagef("--version: %v", err)
	}
	if why := f.unsupported(edition); why != "" {
		return codedError{exitUnsupported, errors.New(why)}
	}
	cert, roots, err := loadKeyPair(tlsDir, "server")
	if err != nil {
		return err
	}
	devices, err := loadDevices(filepath.Join(tlsDir, "devices"))
	if err != nil {
		return err
	}
	readyFile := fs.get("ready-file", defaultReady)
	controlSocket := fs.get("control-socket", defaultControlSocket)

	cfg := server.Config{
		PeerCertificates:   tlsccm.PeerCertificates,
		DefaultEdition:     edition,
		InBandRegistration: true,
		NotificationClient: server.NewNotificationClient(cert, roots),
		// The listeners of the clients are containers of a private Docker
		// network, which the default policy refuses.
		NotificationURIAllowed: func(*url.URL) bool { return true },
		Logger:                 stderrLogger{},
	}
	if f.csipAUS() {
		cfg = csipserver.Configure(cfg)
	}
	srv, err := server.New(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	seeded, err := seed(ctx, srv, f, devices, edition)
	cancel()
	if err != nil {
		return fmt.Errorf("seeding the server: %w", err)
	}

	inner, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	l, err := tlsccm.NewListener(inner, cert, roots, tlsccm.Options{AllowGCM: true, Logger: stderrLogger{}})
	if err != nil {
		_ = inner.Close()
		return err
	}
	hs := &http.Server{Handler: observe(srv), ReadHeaderTimeout: 10 * time.Second, ErrorLog: nil}
	tlsccm.ConfigureServer(hs)
	served := make(chan error, 1)
	go func() { served <- hs.Serve(l) }()

	ctl, err := listenControl(controlSocket, &controller{srv: srv, fixture: f, devices: seeded})
	if err != nil {
		_ = hs.Close()
		return fmt.Errorf("control socket: %w", err)
	}
	defer func() { _ = ctl.Close() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	for _, d := range seeded {
		emit(map[string]any{"event": "device-provisioned", "sfdi": d.SFDI, "lfdi": d.LFDI.String()})
	}
	if err := os.WriteFile(readyFile, nil, 0o644); err != nil {
		_ = hs.Close()
		return fmt.Errorf("ready file: %w", err)
	}
	emit(map[string]any{"event": "ready", "adapter": adapterName, "version": version, "port": port, "fixture": f.Name})

	var cause error
	select {
	case <-signals:
	case err := <-served:
		cause = fmt.Errorf("the HTTPS server stopped: %w", err)
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = hs.Shutdown(shutdown)
	cancel()
	_ = os.Remove(readyFile)
	emit(map[string]any{"event": "stopped"})
	return cause
}

// loadKeyPair reads <name>.crt, <name>.key and ca.crt of dir: exit 4 when
// one is missing or not valid.
func loadKeyPair(dir, name string) (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key"))
	if err != nil {
		return cert, nil, codedError{exitFixture, fmt.Errorf("--tls-dir: %w", err)}
	}
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return cert, nil, codedError{exitFixture, fmt.Errorf("--tls-dir: %s.crt: %w", name, err)}
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return cert, nil, codedError{exitFixture, fmt.Errorf("--tls-dir: %w", err)}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return cert, nil, codedError{exitFixture, fmt.Errorf("--tls-dir: ca.crt holds no certificate")}
	}
	return cert, roots, nil
}

// loadDevices reads the device certificates of dir, sorted by file name.
func loadDevices(dir string) ([]*x509.Certificate, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, codedError{exitFixture, fmt.Errorf("--tls-dir: %w", err)}
	}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".crt") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var certs []*x509.Certificate
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			return nil, codedError{exitFixture, fmt.Errorf("--tls-dir: %w", err)}
		}
		block, _ := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, codedError{exitFixture, fmt.Errorf("--tls-dir: devices/%s is not a PEM certificate", n)}
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, codedError{exitFixture, fmt.Errorf("--tls-dir: devices/%s: %w", n, err)}
		}
		certs = append(certs, c)
	}
	return certs, nil
}

// seed provisions every device on srv with the fixture's PIN and a DER,
// and assigns it the fixture's DER programs (with their default controls
// and controls, which start now plus their start, and answer to a
// ResponseSet) and tariffs, each device its own.
func seed(ctx context.Context, srv *server.Server, f *fixture, devices []*x509.Certificate, edition ieee20305.Edition) ([]seededDevice, error) {
	a := srv.Admin()
	aus := f.csipAUS()
	for _, r := range []struct{ resource, href string }{
		{"DERProgramList", "/derp"}, {"MirrorUsagePointList", "/mup"}, {"ResponseSetList", "/rsps"}, {"TariffProfileList", "/tp"},
	} {
		if err := a.Root(ctx, r.resource, r.href, a.Document(r.resource)); err != nil {
			return nil, err
		}
	}
	rs, err := a.Create(ctx, "/rsps", &ieee20305.ResponseSet{}, "")
	if err != nil {
		return nil, err
	}
	replyTo := rs + "/rsp"
	now := time.Now()
	var out []seededDevice
	for _, cert := range devices {
		lfdi := security.CertificateLFDI(cert)
		d := seededDevice{LFDI: lfdi, SFDI: lfdi.SFDI().String(), Programs: map[string]string{}, ReplyTo: replyTo}
		tmpl := &ieee20305.EndDevice{}
		if f.Device.DeviceCategory != "" {
			dc := ieee20305.DeviceCategoryType(unhex(f.Device.DeviceCategory))
			tmpl.DeviceCategory = &dc
		}
		d.EndDevice, err = a.ProvisionDevice(ctx, server.DeviceSpec{LFDI: lfdi, Edition: edition, Register: true, PIN: f.Device.PIN, EndDevice: tmpl})
		if err != nil {
			return nil, fmt.Errorf("provisioning %s: %w", lfdi, err)
		}
		if _, err := a.Create(ctx, d.EndDevice+"/der", &ieee20305.DER{}, lfdi.String()); err != nil {
			return nil, err
		}
		fsaDoc := &ieee20305.FunctionSetAssignments{MRID: ieee20305.MRIDType{0xF5, 0xA0}}
		fsaDoc.Description = str32("ieee20305-interop")
		if d.FSA, err = a.AssignFSA(ctx, lfdi, fsaDoc); err != nil {
			return nil, err
		}
		if err := a.NewList(ctx, "DERProgramList", d.FSA+"/derp", d.FSA, "DERProgramListLink", lfdi.String()); err != nil {
			return nil, err
		}
		flags := ieee20305.HexBinary8{0x03}
		for _, fp := range f.DERPrograms {
			prog := &ieee20305.DERProgram{Primacy: ieee20305.PrimacyType(fp.Primacy)}
			prog.MRID = unhex(fp.MRID)
			prog.Description = str32(fp.Description)
			href, err := a.Create(ctx, "/derp", prog, "")
			if err != nil {
				return nil, err
			}
			if err := a.AddMember(ctx, d.FSA+"/derp", href); err != nil {
				return nil, err
			}
			d.Programs[fp.Description] = href
			if dc := fp.DefaultControl; dc != nil {
				b, err := base(dc.Base)
				if err != nil {
					return nil, err
				}
				v := &csipaus.DefaultDERControl{DERControlBase: b}
				v.MRID = unhex(dc.MRID)
				v.Description = str32(dc.Description)
				if err := a.Put(ctx, &server.Node{Href: href + "/dderc", Resource: "DefaultDERControl", Value: defaultControl(v, aus)}); err != nil {
					return nil, err
				}
			}
			for _, fc := range fp.Controls {
				b, err := base(fc.Base)
				if err != nil {
					return nil, err
				}
				c := &csipaus.DERControl{DERControlBase: b}
				c.MRID = unhex(fc.MRID)
				c.Description = str32(fc.Description)
				c.CreationTime = ieee20305.TimeType(now.Unix())
				c.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix() + int64(fc.Start)), Duration: ieee20305.UInt32(fc.Duration)}
				c.ReplyTo, c.ResponseRequired = &replyTo, &flags
				if _, err := a.Create(ctx, href+"/derc", derControl(c, aus), ""); err != nil {
					return nil, err
				}
			}
		}
		if err := seedTariffs(ctx, a, f, d.FSA, lfdi, now); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func seedTariffs(ctx context.Context, a *server.Admin, f *fixture, fsa string, lfdi security.LFDI, now time.Time) error {
	if len(f.Tariffs) == 0 {
		return nil
	}
	aus := f.csipAUS()
	if err := a.NewList(ctx, "TariffProfileList", fsa+"/tp", fsa, "TariffProfileListLink", lfdi.String()); err != nil {
		return err
	}
	for _, ft := range f.Tariffs {
		tp := &ieee20305.TariffProfile{Primacy: ieee20305.PrimacyType(ft.Primacy)}
		tp.MRID = unhex(ft.MRID)
		tp.Description = str32(ft.Description)
		cur := ieee20305.CurrencyCode(ft.Currency)
		mult := ieee20305.PowerOfTenMultiplierType(ft.PricePowerOfTenMultiplier)
		tp.Currency, tp.PricePowerOfTenMultiplier = &cur, &mult
		if ft.RateCode != "" {
			rc := ieee20305.String20(ft.RateCode)
			tp.RateCode = &rc
		}
		tph, err := a.Create(ctx, "/tp", tariff(tp, aus), "")
		if err != nil {
			return err
		}
		if err := a.AddMember(ctx, fsa+"/tp", tph); err != nil {
			return err
		}
		for _, frc := range ft.RateComponents {
			rc := &ieee20305.RateComponent{RoleFlags: unhex(frc.RoleFlags)}
			rc.MRID = unhex(frc.MRID)
			rc.Description = str32(frc.Description)
			rch, err := a.Create(ctx, tph+"/rc", rc, "")
			if err != nil {
				return err
			}
			dir := ieee20305.FlowDirectionType(frc.FlowDirection)
			if err := a.Put(ctx, &server.Node{Href: rch + "/rt", Resource: "ReadingType", Value: &ieee20305.ReadingType{FlowDirection: &dir}}); err != nil {
				return err
			}
			n, err := a.Get(ctx, rch)
			if err != nil {
				return err
			}
			n.Value.(*ieee20305.RateComponent).ReadingTypeLink.Href = rch + "/rt"
			if err := a.Put(ctx, n); err != nil {
				return err
			}
			for _, fi := range frc.TimeTariffIntervals {
				tti := &ieee20305.TimeTariffInterval{TouTier: 1}
				tti.MRID = unhex(fi.MRID)
				tti.Description = str32(fi.Description)
				tti.CreationTime = ieee20305.TimeType(now.Unix())
				tti.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now.Unix() + int64(fi.Start)), Duration: ieee20305.UInt32(fi.Duration)}
				ttih, err := a.Create(ctx, rch+"/tti", interval(tti, rch, aus), "")
				if err != nil {
					return err
				}
				price := ieee20305.Int32(fi.Price)
				if _, err := a.Create(ctx, ttih+"/cti", &ieee20305.ConsumptionTariffInterval{ConsumptionBlock: 1, Price: &price}, ""); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// defaultControl and derControl return the documents the server serves:
// those of CSIP-AUS for a CSIP-AUS fixture.
func defaultControl(d *csipaus.DefaultDERControl, aus bool) any {
	if aus {
		return d
	}
	return &ieee20305.DefaultDERControl{RespondableSubscribableIdentifiedObject: d.RespondableSubscribableIdentifiedObject,
		DERControlBase: d.DERControlBase.DERControlBase}
}

func derControl(c *csipaus.DERControl, aus bool) any {
	if aus {
		return c
	}
	return &ieee20305.DERControl{RandomizableEvent: c.RandomizableEvent, DERControlBase: c.DERControlBase.DERControlBase}
}

// tariff and interval return the documents of a TariffProfile and a
// TimeTariffInterval of rate component rc: those of CSIP-AUS, whose
// TimeTariffInterval links its RateComponent, for a CSIP-AUS fixture.
func tariff(tp *ieee20305.TariffProfile, aus bool) any {
	if aus {
		return &csipaus.TariffProfile{TariffProfile: *tp}
	}
	return tp
}

func interval(tti *ieee20305.TimeTariffInterval, rc string, aus bool) any {
	if aus {
		return &csipaus.TimeTariffInterval{TimeTariffInterval: *tti, RateComponentLink: ieee20305.RateComponentLink{Link: ieee20305.Link{Href: rc}}}
	}
	return tti
}

// observe writes the events device-registered and subscription-created
// for the server's 201 answers to a device's POST of an EndDevice or a
// Subscription: the library has no hook that observes its built-in
// handling without replacing it.
func observe(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			h.ServeHTTP(w, r)
			return
		}
		// The server limits a request body to 1 MiB; read one byte more so
		// that it still sees one that is too large.
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil {
			http.Error(w, "reading the request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		if rec.status != http.StatusCreated {
			return
		}
		certs, err := tlsccm.PeerCertificates(r)
		if err != nil || len(certs) == 0 {
			return
		}
		lfdi := security.CertificateLFDI(certs[0])
		switch root, doc := rootElement(body); root {
		case "EndDevice":
			emit(map[string]any{"event": "device-registered", "sfdi": lfdi.SFDI().String(), "lfdi": lfdi.String()})
		case "Subscription":
			var sub struct {
				SubscribedResource string `xml:"subscribedResource"`
			}
			_ = xml.Unmarshal(doc, &sub)
			emit(map[string]any{"event": "subscription-created", "sfdi": lfdi.SFDI().String(), "resource": sub.SubscribedResource})
		}
	})
}

// rootElement returns the local name of the root element of an XML
// document, and the document.
func rootElement(doc []byte) (string, []byte) {
	d := xml.NewDecoder(bytes.NewReader(doc))
	for {
		tok, err := d.Token()
		if err != nil {
			return "", doc
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local, doc
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

// stderrLogger is the ieee20305.Logger of the adapter: stderr, for humans.
type stderrLogger struct{}

func (stderrLogger) Debugf(string, ...any) {}
func (stderrLogger) Infof(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ieee20305-interop: "+format+"\n", a...)
}
func (stderrLogger) Warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ieee20305-interop: warning: "+format+"\n", a...)
}
func (stderrLogger) Errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ieee20305-interop: error: "+format+"\n", a...)
}
