// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GRIDAPPSD/ieee-2030_5-client-go/internal/inverter"
	"github.com/GRIDAPPSD/ieee-2030_5-core-go/pkg/sep2"
)

// result is the result document of a one-shot client operation.
type result struct {
	Operation string         `json:"operation"`
	OK        bool           `json:"ok"`
	Error     *resultError   `json:"error"`
	Request   map[string]any `json:"request"`
	Response  map[string]any `json:"response"`
}

type resultError struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// opEnv is what an operation works with.
type opEnv struct {
	c            *inverter.SEP2Client
	f            *Fixture
	cfg          inverter.SimConfig // of c
	tlsDir       string
	notifyListen string
	notifyHost   string
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
	"poll-pricing":     nil, // unsupported: the upstream has no pricing types
}

func runClient(args []string) int {
	if len(args) == 0 {
		return exitCode(usagef("client needs an operation"))
	}
	op := args[0]
	if op == "run" {
		return exitCode(codedError{exitUnsupported, errors.New("client run is not implemented by the gridappsd adapter")})
	}
	fs, err := parseFlags(args[1:], "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload",
		"notify-listen", "notify-host")
	if err != nil {
		return exitCode(err)
	}
	edition, err := fs.edition()
	if err != nil {
		return exitCode(err)
	}
	rawURL, err := fs.required("url")
	if err != nil {
		return exitCode(err)
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
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
	fixture, _, err := loadFixture(fs.get("fixture", defaultFixture))
	if err != nil {
		return exitCode(err)
	}

	res := &result{Operation: op}
	start := time.Now()
	var code int
	if why := clientUnsupported(op, fixture); why != "" {
		res.Error = &resultError{Code: "unsupported", Detail: why}
		code = exitUnsupported
	} else {
		env := &opEnv{f: fixture, tlsDir: tlsDir, notifyListen: notifyListen, notifyHost: notifyHost}
		code = perform(op, u, env, connectTimeout, timeout, res)
	}
	res.OK = code == exitOK
	emit(map[string]any{
		"schemaVersion": "1.0",
		"adapter":       adapterName,
		"version":       edition,
		"operation":     res.Operation,
		"ok":            res.OK,
		"error":         res.Error,
		"request":       res.Request,
		"response":      res.Response,
		"elapsedMs":     time.Since(start).Milliseconds(),
	})
	return code
}

func perform(op string, u *url.URL, env *opEnv, connectTimeout, timeout time.Duration, res *result) int {
	// The upstream client dials through its own TLS stack; check first that
	// the server can be reached at all, so that an unreachable server is a
	// connect failure rather than an operation failure.
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	conn, err := net.DialTimeout("tcp", host, connectTimeout)
	if err != nil {
		res.Error = &resultError{Code: "connect-failed", Detail: err.Error()}
		return exitConnect
	}
	conn.Close()

	env.cfg = inverter.SimConfig{
		ServerURL: "https://" + host,
		CertFile:  filepath.Join(env.tlsDir, "client.crt"),
		KeyFile:   filepath.Join(env.tlsDir, "client.key"),
		CAFile:    filepath.Join(env.tlsDir, "ca.crt"),
	}
	c, err := inverter.NewSEP2Client(env.cfg)
	if err != nil {
		res.Error = &resultError{Code: "invalid-request", Detail: err.Error()}
		return exitFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	env.c = c
	err = operations[op](ctx, env, res)
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
	case isTLSFailure(err):
		res.Error = &resultError{Code: "connect-failed", Detail: err.Error()}
		return exitConnect
	case errors.Is(err, context.DeadlineExceeded):
		res.Error = &resultError{Code: "timeout", Detail: err.Error()}
	default:
		res.Error = &resultError{Code: "server-error", Detail: err.Error()}
	}
	return exitFailed
}

// isTLSFailure reports whether err is a failed TLS handshake: the peer
// refused the certificate, or the certificate of the peer did not verify.
func isTLSFailure(err error) bool {
	var alert tls.AlertError
	var opErr *net.OpError
	var verify *tls.CertificateVerificationError
	if errors.As(err, &alert) || errors.As(err, &verify) || (errors.As(err, &opErr) && opErr.Op == "remote error") {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "tls:") || strings.Contains(s, "x509:") || strings.Contains(s, "handshake")
}

func opDiscover(ctx context.Context, env *opEnv, res *result) error {
	c := env.c
	res.Request = map[string]any{"method": "GET", "path": "/dcap"}
	dc, err := c.Discover(ctx)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 200, "body": dc}
	return nil
}

// ownEndDevice finds the device's EndDevice, posting one when the server
// has none, as a device registering itself does.
func ownEndDevice(ctx context.Context, c *inverter.SEP2Client) (sep2.EndDevice, bool, error) {
	dc, err := c.Discover(ctx)
	if err != nil {
		return sep2.EndDevice{}, false, err
	}
	if dc.EndDeviceListLink == nil {
		return sep2.EndDevice{}, false, errors.New("the DeviceCapability has no EndDeviceListLink")
	}
	ed, _, err := c.LookupOwnEndDevice(ctx, dc.EndDeviceListLink.Href)
	if err == nil {
		return ed, false, nil
	}
	if !errors.Is(err, inverter.ErrEndDeviceNotFound) {
		return sep2.EndDevice{}, false, err
	}
	ed, _, err = c.Register(ctx, dc.EndDeviceListLink.Href)
	return ed, true, err
}

func opRegister(ctx context.Context, env *opEnv, res *result) error {
	c, f := env.c, env.f
	res.Request = map[string]any{"method": "GET", "path": "/edev"}
	ed, created, err := ownEndDevice(ctx, c)
	if created {
		res.Request = map[string]any{"method": "POST", "path": "/edev"}
	}
	if err != nil {
		return err
	}
	body := map[string]any{"endDevice": ed, "created": created}
	status := 200
	if created {
		status = 201
	}
	res.Response = map[string]any{"status": status, "body": body}
	if ed.RegistrationLink == nil {
		return errors.New("the server did not register the device: its EndDevice has no RegistrationLink")
	}
	reg, err := c.GetRegistration(ctx, ed.RegistrationLink.Href)
	if err != nil {
		return err
	}
	body["registration"] = reg
	if reg.PIN != f.Device.PIN {
		return fmt.Errorf("the Registration has PIN %d, the device was given %d", reg.PIN, f.Device.PIN)
	}
	return nil
}

func opReport(ctx context.Context, env *opEnv, res *result) error {
	c, f := env.c, env.f
	dc, err := c.Discover(ctx)
	if err != nil {
		return err
	}
	if dc.MirrorUsagePointListLink == nil {
		return errors.New("the DeviceCapability has no MirrorUsagePointListLink")
	}
	res.Request = map[string]any{"method": "POST", "path": dc.MirrorUsagePointListLink.Href}
	m := f.Report.MirrorUsagePoint
	roleFlags, err := hex.DecodeString(m.RoleFlags)
	if err != nil || len(roleFlags) != 2 {
		return codedError{exitFixture, fmt.Errorf("fixture: roleFlags %q is not 4 hexadecimal digits", m.RoleFlags)}
	}
	mup := sep2.MirrorUsagePoint{
		MRID:                m.MRID,
		Description:         m.Description,
		RoleFlags:           sep2.RoleFlagsValue(uint16(roleFlags[0])<<8 | uint16(roleFlags[1])),
		ServiceCategoryKind: m.ServiceCategoryKind,
		Status:              m.Status,
	}
	now := time.Now().Unix()
	for _, r := range f.Report.MeterReadings {
		value := r.Value
		rt := &sep2.ReadingType{}
		set8 := func(name string) *uint8 {
			if v, ok := r.ReadingType[name]; ok {
				u := uint8(v)
				return &u
			}
			return nil
		}
		rt.AccumulationBehaviour = set8("accumulationBehaviour")
		rt.Commodity = set8("commodity")
		rt.DataQualifier = set8("dataQualifier")
		rt.FlowDirection = set8("flowDirection")
		rt.Kind = set8("kind")
		rt.Phase = set8("phase")
		rt.Uom = set8("uom")
		if v, ok := r.ReadingType["powerOfTenMultiplier"]; ok {
			p := int8(v)
			rt.PowerOfTenMultiplier = &p
		}
		mup.MirrorMeterReading = append(mup.MirrorMeterReading, sep2.MirrorMeterReading{
			MRID:           r.MRID,
			Description:    r.Description,
			LastUpdateTime: now,
			ReadingType:    rt,
			// An instantaneous reading taken now (duration 0).
			Reading: &sep2.Reading{Value: &value, TimePeriod: &sep2.DateTimeInterval{Start: now}},
		})
	}
	loc, err := c.CreateMirrorUsagePoint(ctx, dc.MirrorUsagePointListLink.Href, inverter.DeviceLFDI(c.LFDI()), mup)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}
