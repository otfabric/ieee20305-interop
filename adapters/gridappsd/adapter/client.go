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

var operations = map[string]func(context.Context, *inverter.SEP2Client, *Fixture, *result) error{
	"discover":      opDiscover,
	"register":      opRegister,
	"poll-controls": opPollControls,
	"report":        opReport,
}

func runClient(args []string) int {
	if len(args) == 0 {
		return exitCode(usagef("client needs an operation"))
	}
	op := args[0]
	if op == "run" {
		return exitCode(codedError{exitUnsupported, errors.New("client run is not implemented by the gridappsd adapter")})
	}
	fs, err := parseFlags(args[1:], "version", "url", "tls-dir", "fixture", "connect-timeout-ms", "timeout-ms", "payload")
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
	if _, known := operations[op]; !known && op != "subscribe" {
		return exitCode(usagef("unknown client operation %q", op))
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
	code := exitOK
	if op == "subscribe" {
		res.Error = &resultError{Code: "unsupported", Detail: "subscriptions are not implemented by the gridappsd adapter"}
		code = exitUnsupported
	} else {
		code = perform(op, u, tlsDir, fixture, connectTimeout, timeout, res)
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

func perform(op string, u *url.URL, tlsDir string, f *Fixture, connectTimeout, timeout time.Duration, res *result) int {
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

	c, err := inverter.NewSEP2Client(inverter.SimConfig{
		ServerURL: "https://" + host,
		CertFile:  filepath.Join(tlsDir, "client.crt"),
		KeyFile:   filepath.Join(tlsDir, "client.key"),
		CAFile:    filepath.Join(tlsDir, "ca.crt"),
	})
	if err != nil {
		res.Error = &resultError{Code: "invalid-request", Detail: err.Error()}
		return exitFailed
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	err = operations[op](ctx, c, f, res)
	switch {
	case err == nil:
		return exitOK
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

func opDiscover(ctx context.Context, c *inverter.SEP2Client, _ *Fixture, res *result) error {
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

func opRegister(ctx context.Context, c *inverter.SEP2Client, f *Fixture, res *result) error {
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

func opPollControls(ctx context.Context, c *inverter.SEP2Client, f *Fixture, res *result) error {
	ed, _, err := ownEndDevice(ctx, c)
	if err != nil {
		return err
	}
	if ed.FunctionSetAssignmentsListLink == nil {
		return errors.New("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	res.Request = map[string]any{"method": "GET", "path": ed.FunctionSetAssignmentsListLink.Href}
	fsas, _, err := c.GetFSAList(ctx, ed.FunctionSetAssignmentsListLink.Href)
	if err != nil {
		return err
	}
	var programs []map[string]any
	seen := map[string]bool{} // descriptions of the programs and controls served
	for _, fsa := range fsas.FunctionSetAssignments {
		if fsa.DERProgramListLink == nil {
			continue
		}
		list, _, err := c.GetDERProgramList(ctx, fsa.DERProgramListLink.Href)
		if err != nil {
			return err
		}
		for _, p := range list.DERProgram {
			entry := map[string]any{"program": p}
			seen["program "+p.Description] = true
			if p.DefaultDERControlLink != nil {
				d, _, err := c.GetDefaultDERControl(ctx, p.DefaultDERControlLink.Href)
				if err != nil {
					return err
				}
				entry["defaultControl"] = d
			}
			if p.DERControlListLink != nil {
				controls, _, err := c.GetDERControlList(ctx, p.DERControlListLink.Href)
				if err != nil {
					return err
				}
				entry["controls"] = controls.DERControl
				for _, dc := range controls.DERControl {
					seen["control "+p.Description+"/"+dc.Description] = true
				}
				if len(controls.Rejected) > 0 {
					entry["rejected"] = controls.Rejected
				}
			}
			programs = append(programs, entry)
		}
	}
	if len(programs) == 0 {
		return errors.New("the device's function set assignments hold no DER program")
	}
	res.Response = map[string]any{"status": 200, "body": map[string]any{"derPrograms": programs}}
	// The server was seeded with the fixture's programs and controls.
	for _, p := range f.DERPrograms {
		if !seen["program "+p.Description] {
			return fmt.Errorf("the server does not serve the fixture's DER program %q", p.Description)
		}
		for _, dc := range p.Controls {
			if !seen["control "+p.Description+"/"+dc.Description] {
				return fmt.Errorf("the server does not serve the fixture's control %q of %q", dc.Description, p.Description)
			}
		}
	}
	return nil
}

func opReport(ctx context.Context, c *inverter.SEP2Client, f *Fixture, res *result) error {
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
			Reading:        &sep2.Reading{Value: &value},
		})
	}
	loc, err := c.CreateMirrorUsagePoint(ctx, dc.MirrorUsagePointListLink.Href, inverter.DeviceLFDI(c.LFDI()), mup)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}
