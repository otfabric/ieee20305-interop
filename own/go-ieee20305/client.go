// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/tlsccm"
)

// result is the result document of a one-shot client operation.
type result struct {
	Request  map[string]any
	Response map[string]any
	Error    *resultError
}

type resultError struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// opEnv is what an operation works with.
type opEnv struct {
	f            *fixture
	edition      ieee20305.Edition
	server       *url.URL // scheme and host of --url
	dcapPath     string
	tlsDir       string
	device       tls.Certificate
	roots        *x509.CertPool
	id           client.Identity
	c            *client.Client
	notifyListen string
	notifyHost   string
}

// newClient returns a client of the server with the codec.
func (env *opEnv) newClient(codec client.Codec) (*client.Client, error) {
	return client.New(client.Config{
		Server:               env.server.String(),
		DeviceCapabilityPath: env.dcapPath,
		HTTPClient:           tlsccm.NewHTTPClient(env.device, env.roots, tlsccm.Options{AllowGCM: true}),
		Codec:                codec,
		UserAgent:            "ieee20305-interop-otfabric/" + adapterVersion,
	})
}

var operations = map[string]func(context.Context, *opEnv, *result) error{
	"discover":         opDiscover,
	"register":         opRegister,
	"poll-controls":    opPollControls,
	"report":           opReport,
	"subscribe":        opSubscribe,
	"respond":          opRespond,
	"log-event":        opLogEvent,
	"put-der":          opPutDER,
	"flow-reservation": opFlowReservation,
	"poll-pricing":     opPollPricing,
}

func runClient(args []string) int {
	if len(args) == 0 {
		return exitCode(usagef("client needs an operation"))
	}
	op := args[0]
	if op == "run" {
		return exitCode(codedError{exitUnsupported, errors.New("client run is not implemented by the otfabric adapter")})
	}
	fs, err := parseFlags(args[1:], "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload",
		"notify-listen", "notify-host")
	if err != nil {
		return exitCode(err)
	}
	version, err := fs.edition()
	if err != nil {
		return exitCode(err)
	}
	rawURL, err := fs.required("url")
	if err != nil {
		return exitCode(err)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return exitCode(usagef("--url %q is not an https:// URL", rawURL))
	}
	connectTimeout, err := fs.millis("connect-timeout-ms", 10*time.Second)
	if err != nil {
		return exitCode(err)
	}
	timeout, err := fs.millis("timeout-ms", 30*time.Second)
	if err != nil {
		return exitCode(err)
	}
	if _, known := operations[op]; !known {
		return exitCode(usagef("unknown client operation %q", op))
	}
	notifyListen := fs.get("notify-listen", "0.0.0.0:8443")
	_, listenPort, err := net.SplitHostPort(notifyListen)
	if err != nil || listenPort == "" {
		return exitCode(usagef("--notify-listen %q is not host:port", notifyListen))
	}
	notifyHost, ok := fs.values["notify-host"]
	if !ok {
		host, _ := os.Hostname()
		notifyHost = net.JoinHostPort(host, listenPort)
	} else if notifyHost == "" || strings.ContainsAny(notifyHost, "/?#@ ") {
		return exitCode(usagef("--notify-host %q is not HOST[:PORT]", notifyHost))
	}
	tlsDir, err := fs.required("tls-dir")
	if err != nil {
		return exitCode(err)
	}
	f, err := loadFixture(fs.get("fixture", defaultFixture))
	if err != nil {
		return exitCode(err)
	}
	edition, err := ieee20305.ParseEdition(version)
	if err != nil {
		return exitCode(usagef("--version: %v", err))
	}

	res := &result{}
	start := time.Now()
	var code int
	if why := clientUnsupported(op, f, edition); why != "" {
		res.Error = &resultError{Code: "unsupported", Detail: why}
		code = exitUnsupported
	} else {
		env := &opEnv{f: f, edition: edition, tlsDir: tlsDir, notifyListen: notifyListen, notifyHost: notifyHost,
			server: &url.URL{Scheme: "https", Host: u.Host}, dcapPath: u.Path}
		code = perform(op, env, connectTimeout, timeout, res)
	}
	emit(map[string]any{
		"schemaVersion": "1.0",
		"adapter":       adapterName,
		"version":       version,
		"operation":     op,
		"ok":            code == exitOK,
		"error":         res.Error,
		"request":       res.Request,
		"response":      res.Response,
		"elapsedMs":     time.Since(start).Milliseconds(),
	})
	return code
}

// clientUnsupported says why the adapter cannot perform op with the
// fixture f in edition, or "".
func clientUnsupported(op string, f *fixture, edition ieee20305.Edition) string {
	if why := f.unsupported(edition); why != "" {
		return why
	}
	switch {
	case op == "put-der" && f.DER == nil:
		return "the fixture " + f.Name + " has no der"
	case op == "log-event" && f.LogEvent == nil:
		return "the fixture " + f.Name + " has no logEvent"
	case op == "flow-reservation" && f.FlowReservation == nil:
		return "the fixture " + f.Name + " has no flowReservation"
	case op == "report" && len(f.Report.MeterReadings) == 0:
		return "the fixture " + f.Name + " has no meterReadings"
	}
	return ""
}

func perform(op string, env *opEnv, connectTimeout, timeout time.Duration, res *result) int {
	var err error
	env.device, env.roots, err = loadKeyPair(env.tlsDir, "client")
	if err != nil {
		res.Error = &resultError{Code: "invalid-request", Detail: err.Error()}
		return exitFixture
	}
	env.id = client.IdentityOf(env.device.Leaf)
	// Reach the server first, so that an unreachable server is a connect
	// failure within --connect-timeout-ms rather than an operation failure.
	host := env.server.Host
	if env.server.Port() == "" {
		host = net.JoinHostPort(env.server.Hostname(), "443")
		env.server.Host = host
	}
	conn, err := net.DialTimeout("tcp", host, connectTimeout)
	if err != nil {
		res.Error = &resultError{Code: "connect-failed", Detail: err.Error()}
		return exitConnect
	}
	_ = conn.Close()
	if env.c, err = env.newClient(env.f.codec(env.edition)); err != nil {
		res.Error = &resultError{Code: "invalid-request", Detail: err.Error()}
		return exitFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err = operations[op](ctx, env, res)
	var status *ieee20305.StatusError
	var coded codedError
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &coded):
		res.Error = &resultError{Code: "invalid-request", Detail: err.Error()}
		if coded.code == exitUnsupported {
			res.Error.Code = "unsupported"
		}
		return coded.code
	case errors.As(err, &status):
		res.Error = &resultError{Code: "server-error", Detail: err.Error()}
	case isConnectFailure(err):
		res.Error = &resultError{Code: "connect-failed", Detail: err.Error()}
		return exitConnect
	case errors.Is(err, ieee20305.ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		res.Error = &resultError{Code: "timeout", Detail: err.Error()}
	default:
		res.Error = &resultError{Code: "server-error", Detail: err.Error()}
	}
	return exitFailed
}

// isConnectFailure reports whether err is a failed TLS handshake or a
// connection the server refused: the client's classification, and the
// errors of the tlsccm stack, whose types are not crypto/tls's.
func isConnectFailure(err error) bool {
	if errors.Is(err, ieee20305.ErrCertificate) || errors.Is(err, ieee20305.ErrUnavailable) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "tls:") || strings.Contains(s, "x509:") || strings.Contains(s, "handshake")
}

func opDiscover(ctx context.Context, env *opEnv, res *result) error {
	res.Request = map[string]any{"method": "GET", "path": env.dcapPath}
	dcap, err := env.c.DeviceCapability(ctx)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 200, "body": dcap}
	return nil
}

// endDevice finds the device's EndDevice, posting one when the server has
// none, as a device registering itself does.
func endDevice(ctx context.Context, env *opEnv) (*ieee20305.DeviceCapability, *ieee20305.EndDevice, bool, error) {
	dcap, err := env.c.DeviceCapability(ctx)
	if err != nil {
		return nil, nil, false, err
	}
	if dcap.EndDeviceListLink == nil {
		return dcap, nil, false, errors.New("the DeviceCapability has no EndDeviceListLink")
	}
	ed, created, err := env.c.Register(ctx, dcap.EndDeviceListLink.Href, env.id)
	return dcap, ed, created, err
}

func opRegister(ctx context.Context, env *opEnv, res *result) error {
	res.Request = map[string]any{"method": "GET", "path": env.dcapPath}
	_, ed, created, err := endDevice(ctx, env)
	if ed != nil {
		method := "GET"
		if created {
			method = "POST"
		}
		res.Request = map[string]any{"method": method, "path": ed.GetHref()}
		status := 200
		if created {
			status = 201
		}
		res.Response = map[string]any{"status": status, "body": map[string]any{"endDevice": ed, "created": created}}
	}
	if err != nil {
		return err
	}
	reg, err := env.c.Registration(ctx, ed)
	if err != nil {
		return err
	}
	res.Response["body"].(map[string]any)["registration"] = reg
	return client.CheckPIN(reg, env.f.Device.PIN)
}

func opReport(ctx context.Context, env *opEnv, res *result) error {
	dcap, err := env.c.DeviceCapability(ctx)
	if err != nil {
		return err
	}
	if dcap.MirrorUsagePointListLink == nil {
		return errors.New("the DeviceCapability has no MirrorUsagePointListLink")
	}
	res.Request = map[string]any{"method": "POST", "path": dcap.MirrorUsagePointListLink.Href}
	loc, err := env.c.Post(ctx, dcap.MirrorUsagePointListLink.Href, env.f.mirrorUsagePoint(env.id))
	if err != nil {
		return err
	}
	if loc == "" {
		return errors.New("the server created no MirrorUsagePoint: no Location")
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}

// servedControl is a DERControl a server serves.
type servedControl struct {
	MRID     string `json:"mRID"`
	base     *csipaus.DERControlBase
	Start    int64  `json:"start"`
	Duration uint32 `json:"duration"`
	ReplyTo  string `json:"replyTo,omitempty"`
	mrid     ieee20305.MRIDType
}

func controlOf(it any) (servedControl, bool) {
	var ev *ieee20305.RandomizableEvent
	var b *csipaus.DERControlBase
	switch x := it.(type) {
	case *ieee20305.DERControl:
		ev, b = &x.RandomizableEvent, &csipaus.DERControlBase{DERControlBase: x.DERControlBase}
	case *csipaus.DERControl:
		ev, b = &x.RandomizableEvent, &x.DERControlBase
	default:
		return servedControl{}, false
	}
	c := servedControl{MRID: fmt.Sprintf("%X", []byte(ev.MRID)), mrid: ev.MRID, base: b, Start: int64(ev.Interval.Start), Duration: uint32(ev.Interval.Duration)}
	if ev.ReplyTo != nil {
		c.ReplyTo = *ev.ReplyTo
	}
	return c, true
}

// servedProgram is a DER program a server serves, with its default control
// and controls.
type servedProgram struct {
	Href     string          `json:"href"`
	Primacy  uint8           `json:"primacy"`
	Default  bool            `json:"defaultControl"`
	Controls []servedControl `json:"controls"`
	dflt     *csipaus.DERControlBase
}

// programs returns the DER programs the server serves to the device of ed,
// by description.
func programs(ctx context.Context, c *client.Client, ed *ieee20305.EndDevice) (map[string]*servedProgram, error) {
	if ed.FunctionSetAssignmentsListLink == nil {
		return nil, errors.New("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	served := map[string]*servedProgram{}
	err := c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 50, func(it any) error {
		fsa, ok := it.(*ieee20305.FunctionSetAssignments)
		if !ok || fsa.DERProgramListLink == nil {
			return nil
		}
		return c.Iterate(ctx, fsa.DERProgramListLink.Href, 50, func(it any) error {
			prog, ok := it.(*ieee20305.DERProgram)
			if !ok {
				return nil
			}
			sp := &servedProgram{Href: prog.GetHref(), Primacy: uint8(prog.Primacy)}
			served[describe(prog.Description)] = sp
			if prog.DefaultDERControlLink != nil {
				doc, err := c.Fetch(ctx, prog.DefaultDERControlLink.Href)
				if err != nil && !errors.Is(err, ieee20305.ErrNotFound) {
					return err
				}
				switch d := doc.(type) {
				case *ieee20305.DefaultDERControl:
					sp.dflt = &csipaus.DERControlBase{DERControlBase: d.DERControlBase}
				case *csipaus.DefaultDERControl:
					sp.dflt = &d.DERControlBase
				}
				sp.Default = sp.dflt != nil
			}
			if prog.DERControlListLink == nil {
				return nil
			}
			return c.Iterate(ctx, prog.DERControlListLink.Href, 50, func(it any) error {
				if dc, ok := controlOf(it); ok {
					sp.Controls = append(sp.Controls, dc)
				}
				return nil
			})
		})
	})
	return served, err
}

// find returns the served control of the fixture's control wc: of its
// DERControlBase values and duration.
func find(controls []servedControl, wc control) (servedControl, bool) {
	for _, dc := range controls {
		if dc.Duration == wc.Duration && sameBase(dc.base, wc.Base) {
			return dc, true
		}
	}
	return servedControl{}, false
}

func opPollControls(ctx context.Context, env *opEnv, res *result) error {
	_, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	if ed.FunctionSetAssignmentsListLink != nil {
		res.Request = map[string]any{"method": "GET", "path": ed.FunctionSetAssignmentsListLink.Href}
	}
	served, err := programs(ctx, env.c, ed)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 200, "body": map[string]any{"programs": served}}
	var missing []string
	for _, want := range env.f.DERPrograms {
		sp, ok := served[want.Description]
		if !ok {
			missing = append(missing, fmt.Sprintf("no DER program %q", want.Description))
			continue
		}
		if sp.Primacy != want.Primacy {
			missing = append(missing, fmt.Sprintf("program %q has primacy %d, want %d", want.Description, sp.Primacy, want.Primacy))
		}
		if want.DefaultControl != nil && (sp.dflt == nil || !sameBase(sp.dflt, want.DefaultControl.Base)) {
			missing = append(missing, fmt.Sprintf("program %q has not the default control %v", want.Description, want.DefaultControl.Base))
		}
		for _, wc := range want.Controls {
			if _, ok := find(sp.Controls, wc); !ok {
				missing = append(missing, fmt.Sprintf("program %q has no control %q (%v for %d s)", want.Description, wc.Description, wc.Base, wc.Duration))
			}
		}
	}
	if len(missing) > 0 {
		return errors.New(strings.Join(missing, "; "))
	}
	return nil
}

func opRespond(ctx context.Context, env *opEnv, res *result) error {
	_, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	served, err := programs(ctx, env.c, ed)
	if err != nil {
		return err
	}
	var ctl *servedControl
	for _, p := range env.f.DERPrograms {
		if served[p.Description] == nil {
			continue
		}
		for _, wc := range p.Controls {
			if wc.Start != 0 {
				continue
			}
			if dc, ok := find(served[p.Description].Controls, wc); ok && ctl == nil {
				ctl = &dc
			}
		}
	}
	if ctl == nil {
		return errors.New("the server serves no control of the fixture that is active now")
	}
	if ctl.ReplyTo == "" {
		return fmt.Errorf("the active control %s asks for no response (no replyTo)", ctl.MRID)
	}
	status := ieee20305.UInt8(2)
	now := ieee20305.TimeType(time.Now().Unix())
	r := &ieee20305.DERControlResponse{Response: ieee20305.Response{CreatedDateTime: &now,
		EndDeviceLFDI: ieee20305.HexBinary160(env.id.LFDI[:]), Status: &status, Subject: ctl.mrid}}
	res.Request = map[string]any{"method": "POST", "path": ctl.ReplyTo}
	loc, err := env.c.Post(ctx, ctl.ReplyTo, r)
	if err != nil {
		return err
	}
	if loc == "" {
		res.Response = map[string]any{"status": 204}
	} else {
		res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc, "subject": ctl.MRID}}
	}
	return nil
}

func opLogEvent(ctx context.Context, env *opEnv, res *result) error {
	_, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	if ed.LogEventListLink == nil {
		return errors.New("the EndDevice has no LogEventListLink")
	}
	res.Request = map[string]any{"method": "POST", "path": ed.LogEventListLink.Href}
	loc, err := env.c.Post(ctx, ed.LogEventListLink.Href, env.f.logEvent())
	if err != nil {
		return err
	}
	if loc == "" {
		return errors.New("the server created no LogEvent: no Location")
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}

func opPutDER(ctx context.Context, env *opEnv, res *result) error {
	_, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	if ed.DERListLink == nil {
		return errors.New("the EndDevice has no DERListLink")
	}
	var der *ieee20305.DER
	err = env.c.Iterate(ctx, ed.DERListLink.Href, 1, func(it any) error {
		if d, ok := it.(*ieee20305.DER); ok && der == nil {
			der = d
		}
		return nil
	})
	if err != nil {
		return err
	}
	if der == nil {
		return errors.New("the EndDevice has no DER")
	}
	capability, settings, status, availability := env.f.ders()
	c := env.c
	if _, aus := capability.(*csipaus.DERCapability); aus {
		if env.edition == ieee20305.Edition2018 {
			// CSIP-AUS elements, in the profile of IEEE 2030.5-2018.
			if c, err = env.newClient(csipaus.Codec{}); err != nil {
				return err
			}
		} else {
			// CSIP-AUS profiles IEEE 2030.5-2018 only: without its elements.
			capability = &capability.(*csipaus.DERCapability).DERCapability
			settings = &settings.(*csipaus.DERSettings).DERSettings
		}
	}
	links := map[string]*ieee20305.Link{}
	if der.DERCapabilityLink != nil {
		links["DERCapability"] = &der.DERCapabilityLink.Link
	}
	if der.DERSettingsLink != nil {
		links["DERSettings"] = &der.DERSettingsLink.Link
	}
	if der.DERStatusLink != nil {
		links["DERStatus"] = &der.DERStatusLink.Link
	}
	if der.DERAvailabilityLink != nil {
		links["DERAvailability"] = &der.DERAvailabilityLink.Link
	}
	put := []string{}
	for _, p := range []struct {
		name string
		v    any
	}{{"DERCapability", capability}, {"DERSettings", settings}, {"DERStatus", status}, {"DERAvailability", availability}} {
		l := links[p.name]
		if l == nil || l.Href == "" {
			return fmt.Errorf("the DER has no %sLink", p.name)
		}
		res.Request = map[string]any{"method": "PUT", "path": l.Href}
		if err := c.Put(ctx, l.Href, p.v); err != nil {
			return fmt.Errorf("PUT of the %s: %w", p.name, err)
		}
		put = append(put, l.Href)
	}
	res.Response = map[string]any{"status": 204, "body": map[string]any{"put": put}}
	return nil
}

func opFlowReservation(ctx context.Context, env *opEnv, res *result) error {
	_, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	if ed.FlowReservationRequestListLink == nil {
		return errors.New("the EndDevice has no FlowReservationRequestListLink")
	}
	res.Request = map[string]any{"method": "POST", "path": ed.FlowReservationRequestListLink.Href}
	loc, err := env.c.Post(ctx, ed.FlowReservationRequestListLink.Href, env.f.flowReservation())
	if err != nil {
		return err
	}
	if loc == "" {
		return errors.New("the server created no FlowReservationRequest: no Location")
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}

func opPollPricing(ctx context.Context, env *opEnv, res *result) error {
	dcap, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	c := env.c
	var lists []string
	if ed.FunctionSetAssignmentsListLink != nil {
		err := c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 50, func(it any) error {
			if fsa, ok := it.(*ieee20305.FunctionSetAssignments); ok && fsa.TariffProfileListLink != nil {
				lists = append(lists, fsa.TariffProfileListLink.Href)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if len(lists) == 0 && dcap.TariffProfileListLink != nil {
		lists = append(lists, dcap.TariffProfileListLink.Href)
	}
	if len(lists) == 0 {
		return errors.New("neither the FunctionSetAssignments nor the DeviceCapability link a TariffProfileList")
	}
	res.Request = map[string]any{"method": "GET", "path": lists[0]}
	// description of tariff -> of rate component -> "duration/price" of its
	// intervals.
	served := map[string]map[string][]string{}
	tariffs := map[string]*ieee20305.TariffProfile{}
	for _, l := range lists {
		err := c.Iterate(ctx, l, 50, func(it any) error {
			var tp *ieee20305.TariffProfile
			switch x := it.(type) {
			case *ieee20305.TariffProfile:
				tp = x
			case *csipaus.TariffProfile:
				tp = &x.TariffProfile
			default:
				return nil
			}
			name := describe(tp.Description)
			tariffs[name], served[name] = tp, map[string][]string{}
			if tp.RateComponentListLink == nil {
				return nil
			}
			return c.Iterate(ctx, tp.RateComponentListLink.Href, 50, func(it any) error {
				rc, ok := it.(*ieee20305.RateComponent)
				if !ok || rc.TimeTariffIntervalListLink.Href == "" {
					return nil
				}
				rcName := describe(rc.Description)
				if _, ok := served[name][rcName]; !ok {
					served[name][rcName] = []string{}
				}
				return c.Iterate(ctx, rc.TimeTariffIntervalListLink.Href, 50, func(it any) error {
					var tti *ieee20305.TimeTariffInterval
					switch x := it.(type) {
					case *ieee20305.TimeTariffInterval:
						tti = x
					case *csipaus.TimeTariffInterval:
						tti = &x.TimeTariffInterval
					default:
						return nil
					}
					price := "none"
					if tti.ConsumptionTariffIntervalListLink != nil {
						var ctis ieee20305.ConsumptionTariffIntervalList
						if err := c.GetList(ctx, tti.ConsumptionTariffIntervalListLink.Href, client.ListOptions{Limit: 1}, &ctis); err != nil {
							return err
						}
						if len(ctis.ConsumptionTariffInterval) > 0 && ctis.ConsumptionTariffInterval[0].Price != nil {
							price = fmt.Sprint(*ctis.ConsumptionTariffInterval[0].Price)
						}
					}
					served[name][rcName] = append(served[name][rcName], fmt.Sprintf("%d/%s", tti.Interval.Duration, price))
					return nil
				})
			})
		})
		if err != nil {
			return err
		}
	}
	res.Response = map[string]any{"status": 200, "body": map[string]any{"tariffs": served}}
	var missing []string
	for _, want := range env.f.Tariffs {
		tp, ok := tariffs[want.Description]
		if !ok {
			missing = append(missing, fmt.Sprintf("no tariff %q", want.Description))
			continue
		}
		if tp.Currency == nil || uint16(*tp.Currency) != want.Currency ||
			tp.PricePowerOfTenMultiplier == nil || int8(*tp.PricePowerOfTenMultiplier) != want.PricePowerOfTenMultiplier {
			missing = append(missing, fmt.Sprintf("tariff %q has not currency %d and multiplier %d", want.Description, want.Currency, want.PricePowerOfTenMultiplier))
		}
		for _, rc := range want.RateComponents {
			got, ok := served[want.Description][rc.Description]
			if !ok {
				missing = append(missing, fmt.Sprintf("tariff %q has no rate component %q", want.Description, rc.Description))
				continue
			}
			for _, wi := range rc.TimeTariffIntervals {
				if !slices.Contains(got, fmt.Sprintf("%d/%d", wi.Duration, wi.Price)) {
					missing = append(missing, fmt.Sprintf("rate component %q has no interval %q (%d s at %d)", rc.Description, wi.Description, wi.Duration, wi.Price))
				}
			}
		}
	}
	if len(missing) > 0 {
		return errors.New(strings.Join(missing, "; "))
	}
	return nil
}

// opSubscribe serves the notification listener, subscribes the device to
// the DERProgramList of its first FunctionSetAssignments, and waits for a
// notification until the operation's timeout.
func opSubscribe(ctx context.Context, env *opEnv, res *result) error {
	_, ed, _, err := endDevice(ctx, env)
	if err != nil {
		return err
	}
	if ed.SubscriptionListLink == nil {
		return errors.New("the EndDevice has no SubscriptionListLink")
	}
	if ed.FunctionSetAssignmentsListLink == nil {
		return errors.New("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	var fsa *ieee20305.FunctionSetAssignments
	err = env.c.Iterate(ctx, ed.FunctionSetAssignmentsListLink.Href, 1, func(it any) error {
		if x, ok := it.(*ieee20305.FunctionSetAssignments); ok && fsa == nil {
			fsa = x
		}
		return nil
	})
	if err != nil {
		return err
	}
	if fsa == nil || fsa.DERProgramListLink == nil {
		return errors.New("no FunctionSetAssignments with a DERProgramListLink")
	}
	resource := fsa.DERProgramListLink.Href

	notes := make(chan *ieee20305.Notification, 8)
	h, err := client.NewNotificationHandler(func(_ context.Context, n *ieee20305.Notification) error {
		select {
		case notes <- n:
		default:
		}
		return nil
	}, client.NotificationConfig{Codec: env.c.Codec()})
	if err != nil {
		return err
	}
	listenerCert := env.device
	if c, err := tls.LoadX509KeyPair(filepath.Join(env.tlsDir, "notify.crt"), filepath.Join(env.tlsDir, "notify.key")); err == nil {
		listenerCert = c
	}
	inner, err := net.Listen("tcp", env.notifyListen)
	if err != nil {
		return codedError{exitFailed, fmt.Errorf("notification listener on %s: %w", env.notifyListen, err)}
	}
	// A server authenticates with its certificate when it presents one,
	// which must chain to ca.crt (with any key usage: a server certificate
	// need not say clientAuth). Some deliver without (sep2server), so none
	// is required.
	l := tls.NewListener(inner, &tls.Config{MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{listenerCert}, ClientAuth: tls.RequestClientCert,
		VerifyConnection: func(cs tls.ConnectionState) error { return verifyPeer(cs, env.roots) }})
	mux := http.NewServeMux()
	mux.Handle("/notify", h)
	hs := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(l) }()
	defer func() { _ = hs.Close() }()

	uri := "https://" + env.notifyHost + "/notify"
	res.Request = map[string]any{"method": "POST", "path": ed.SubscriptionListLink.Href}
	loc, err := env.c.Subscribe(ctx, ed.SubscriptionListLink.Href, resource, uri, 1)
	if err != nil {
		return err
	}
	body := map[string]any{"location": loc, "subscribedResource": resource, "notificationURI": uri}
	res.Response = map[string]any{"status": 201, "body": body}
	select {
	case n := <-notes:
		body["notification"] = map[string]any{"subscribedResource": n.SubscribedResource, "status": n.Status}
		if n.SubscribedResource != resource {
			return fmt.Errorf("a notification of %s, want %s", n.SubscribedResource, resource)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: no notification arrived", ieee20305.ErrTimeout)
	}
}

// verifyPeer verifies the certificate a peer presented, if any, against
// roots, for any key usage.
func verifyPeer(cs tls.ConnectionState, roots *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return nil
	}
	inter := x509.NewCertPool()
	for _, c := range cs.PeerCertificates[1:] {
		inter.AddCert(c)
	}
	_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
}
