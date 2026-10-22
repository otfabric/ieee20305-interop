// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/GRIDAPPSD/ieee-2030_5-client-go/internal/inverter"
	"github.com/GRIDAPPSD/ieee-2030_5-client-go/internal/inverter/guard"
	"github.com/GRIDAPPSD/ieee-2030_5-core-go/pkg/sep2"
)

// csipAusElements are the CSIP-AUS DERControlBase elements (csipaus
// namespace): the upstream's core types (ieee-2030_5-core-go
// pkg/sep2.DERControlBase) have no field for them, so the client can neither
// read nor check them.
var csipAusElements = map[string]bool{"opModExpLimW": true, "opModImpLimW": true, "opModGenLimW": true, "opModLoadLimW": true}

// curveElements are the curve modes of a DERControlBase: their value is the
// mRID of one of the program's curves, served as a DERCurveLink.
var curveElements = map[string]func(*sep2.DERControlBase) *sep2.DERCurveLink{
	"opModVoltVar":  func(b *sep2.DERControlBase) *sep2.DERCurveLink { return b.OpModVoltVar },
	"opModVoltWatt": func(b *sep2.DERControlBase) *sep2.DERCurveLink { return b.OpModVoltWatt },
	"opModFreqWatt": func(b *sep2.DERControlBase) *sep2.DERCurveLink { return b.OpModFreqWatt },
	"opModWattPF":   func(b *sep2.DERControlBase) *sep2.DERCurveLink { return b.OpModWattPF },
}

// clientUnsupported says why the client does not perform op with the
// fixture, or "" when it does.
func clientUnsupported(op string, f *Fixture) string {
	switch op {
	case "poll-pricing":
		return "poll-pricing: the upstream client and its core types (ieee-2030_5-core-go pkg/sep2) have no pricing function set (TariffProfile, RateComponent, TimeTariffInterval)"
	case "poll-controls", "respond":
		for _, p := range f.DERPrograms {
			bases := []map[string]json.RawMessage{}
			if p.DefaultControl != nil {
				bases = append(bases, p.DefaultControl.Base)
			}
			for _, c := range p.Controls {
				bases = append(bases, c.Base)
			}
			for _, b := range bases {
				for name := range b {
					if csipAusElements[name] {
						return fmt.Sprintf("%s: the fixture %s has the CSIP-AUS element %s, which the upstream's core types (ieee-2030_5-core-go pkg/sep2.DERControlBase) cannot read", op, f.Name, name)
					}
				}
			}
		}
	case "log-event":
		if f.LogEvent == nil {
			return "log-event: the fixture has no logEvent"
		}
	case "put-der":
		if f.DER == nil {
			return "put-der: the fixture has no der"
		}
	case "flow-reservation":
		if f.FlowRsv == nil {
			return "flow-reservation: the fixture has no flowReservation"
		}
	}
	return ""
}

// servedProgram is one DER program as the server serves it.
type servedProgram struct {
	Program        sep2.DERProgram
	DefaultControl *sep2.DefaultDERControl
	Controls       []sep2.DERControl
	Curves         map[string]sep2.DERCurve // by href; the curve list's under "list:<n>"
}

// walkPrograms follows the device's FunctionSetAssignments to its DER
// programs, and GETs their default controls, controls and curves.
func walkPrograms(ctx context.Context, c *inverter.SEP2Client, res *result) ([]servedProgram, error) {
	ed, _, err := ownEndDevice(ctx, c)
	if err != nil {
		return nil, err
	}
	if ed.FunctionSetAssignmentsListLink == nil {
		return nil, errors.New("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	res.Request = map[string]any{"method": "GET", "path": ed.FunctionSetAssignmentsListLink.Href}
	fsas, _, err := c.GetFSAList(ctx, ed.FunctionSetAssignmentsListLink.Href)
	if err != nil {
		return nil, err
	}
	var out []servedProgram
	for _, fsa := range fsas.FunctionSetAssignments {
		if fsa.DERProgramListLink == nil {
			continue
		}
		list, _, err := c.GetDERProgramList(ctx, fsa.DERProgramListLink.Href)
		if err != nil {
			return nil, err
		}
		for _, p := range list.DERProgram {
			sp := servedProgram{Program: p, Curves: map[string]sep2.DERCurve{}}
			if p.DefaultDERControlLink != nil {
				d, _, err := c.GetDefaultDERControl(ctx, p.DefaultDERControlLink.Href)
				if err != nil {
					return nil, err
				}
				sp.DefaultControl = &d
			}
			if p.DERControlListLink != nil {
				controls, _, err := c.GetDERControlList(ctx, p.DERControlListLink.Href)
				if err != nil {
					return nil, err
				}
				sp.Controls = controls.DERControl
			}
			if p.DERCurveListLink != nil {
				curves, err := c.GetDERCurveList(ctx, p.DERCurveListLink.Href)
				if err != nil {
					return nil, err
				}
				for i, dc := range curves.DERCurve {
					sp.Curves["list:"+strconv.Itoa(i)] = dc
				}
			}
			// The curves the controls link to.
			bases := []*sep2.DERControlBase{}
			if sp.DefaultControl != nil {
				bases = append(bases, sp.DefaultControl.DERControlBase)
			}
			for _, dc := range sp.Controls {
				bases = append(bases, dc.DERControlBase)
			}
			for _, b := range bases {
				if b == nil {
					continue
				}
				for _, link := range curveElements {
					l := link(b)
					if l == nil || l.Href == "" {
						continue
					}
					if _, seen := sp.Curves[l.Href]; seen {
						continue
					}
					var dc sep2.DERCurve
					if _, err := c.Get(ctx, guard.KindEndDeviceRead, l.Href, &dc); err != nil {
						return nil, fmt.Errorf("GET the curve %s of the program %q: %w", l.Href, p.Description, err)
					}
					sp.Curves[l.Href] = dc
				}
			}
			out = append(out, sp)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("the device's function set assignments hold no DER program")
	}
	return out, nil
}

func opPollControls(ctx context.Context, env *opEnv, res *result) error {
	served, err := walkPrograms(ctx, env.c, res)
	if err != nil {
		return err
	}
	body := []map[string]any{}
	for _, sp := range served {
		body = append(body, map[string]any{"program": sp.Program, "defaultControl": sp.DefaultControl, "controls": sp.Controls, "curves": sp.Curves})
	}
	res.Response = map[string]any{"status": 200, "body": map[string]any{"derPrograms": body}}
	// A server picks its own mRIDs and may describe controls its own way:
	// programs are matched by description, controls by what they do,
	// curves by their content.
	for _, p := range env.f.DERPrograms {
		sp := findProgram(served, p.Description)
		if sp == nil {
			return fmt.Errorf("the server does not serve the fixture's DER program %q", p.Description)
		}
		for _, fc := range p.Curves {
			if !curveServed(sp, fc) {
				return fmt.Errorf("the server does not serve the curve %q of %q (curveType %d), through a control's curve link or the DERCurveListLink", fc.Description, p.Description, fc.CurveType)
			}
		}
		if d := p.DefaultControl; d != nil {
			if sp.DefaultControl == nil {
				return fmt.Errorf("the server serves no DefaultDERControl for %q", p.Description)
			}
			if !baseMatches(p, d.Base, sp.DefaultControl.DERControlBase, sp) {
				return fmt.Errorf("the DefaultDERControl of %q is not the fixture's %q", p.Description, d.Description)
			}
		}
		for _, fc := range p.Controls {
			if servedControl(sp, p, fc) == nil {
				return fmt.Errorf("the server does not serve the fixture's control %q of %q", fc.Description, p.Description)
			}
		}
	}
	return nil
}

func findProgram(served []servedProgram, description string) *servedProgram {
	for i := range served {
		if served[i].Program.Description == description {
			return &served[i]
		}
	}
	return nil
}

// servedControl returns the served DERControl with the fixture control's
// duration and DERControlBase, or nil.
func servedControl(sp *servedProgram, p DERProgram, fc Control) *sep2.DERControl {
	for i, dc := range sp.Controls {
		if dc.Interval != nil && dc.Interval.Duration == fc.Duration && baseMatches(p, fc.Base, dc.DERControlBase, sp) {
			return &sp.Controls[i]
		}
	}
	return nil
}

// baseMatches reports whether a served DERControlBase has exactly the
// fixture's elements and values. A curve element matches when the curve it
// links to is the fixture's curve of that mRID.
func baseMatches(p DERProgram, want map[string]json.RawMessage, b *sep2.DERControlBase, sp *servedProgram) bool {
	if b == nil {
		return false
	}
	got := map[string]any{}
	if b.OpModConnect != nil {
		got["opModConnect"] = *b.OpModConnect
	}
	if b.OpModEnergize != nil {
		got["opModEnergize"] = *b.OpModEnergize
	}
	if b.OpModMaxLimW != nil {
		got["opModMaxLimW"] = float64(*b.OpModMaxLimW)
	}
	if b.OpModFixedW != nil {
		got["opModFixedW"] = float64(*b.OpModFixedW)
	}
	for name, link := range curveElements {
		l := link(b)
		if l == nil {
			continue
		}
		got[name] = "?"
		dc, ok := sp.Curves[l.Href]
		if !ok {
			continue
		}
		for _, fc := range p.Curves {
			if curveMatches(fc, dc) {
				got[name] = fc.MRID
				break
			}
		}
	}
	// Any other element the server sets makes the base differ.
	extra := reflect.ValueOf(*b)
	set := 0
	for i := 0; i < extra.NumField(); i++ {
		if !extra.Field(i).IsNil() {
			set++
		}
	}
	if set != len(got) {
		return false
	}
	wantValues := map[string]any{}
	for name, raw := range want {
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return false
		}
		wantValues[name] = v
	}
	return reflect.DeepEqual(got, wantValues)
}

func curveServed(sp *servedProgram, fc Curve) bool {
	for _, dc := range sp.Curves {
		if curveMatches(fc, dc) {
			return true
		}
	}
	return false
}

func curveMatches(fc Curve, dc sep2.DERCurve) bool {
	if dc.CurveType != fc.CurveType || dc.XMultiplier != fc.XMultiplier || dc.YMultiplier != fc.YMultiplier ||
		dc.YRefType != fc.YRefType || len(dc.CurveData) != len(fc.Points) {
		return false
	}
	for i, pt := range fc.Points {
		if dc.CurveData[i].XValue != pt.X || dc.CurveData[i].YValue != pt.Y {
			return false
		}
	}
	return true
}

func opRespond(ctx context.Context, env *opEnv, res *result) error {
	served, err := walkPrograms(ctx, env.c, res)
	if err != nil {
		return err
	}
	var target *sep2.DERControl
	var what string
	for _, p := range env.f.DERPrograms {
		for _, fc := range p.Controls {
			if fc.Start != 0 {
				continue
			}
			what = fmt.Sprintf("%q of %q", fc.Description, p.Description)
			sp := findProgram(served, p.Description)
			if sp == nil {
				return fmt.Errorf("the server does not serve the fixture's DER program %q", p.Description)
			}
			if target = servedControl(sp, p, fc); target == nil {
				return fmt.Errorf("the server does not serve the fixture's control %s", what)
			}
			break
		}
		if target != nil {
			break
		}
	}
	if what == "" {
		return codedError{exitUnsupported, fmt.Errorf("respond: the fixture %s has no control with start 0", env.f.Name)}
	}
	if target.ReplyTo == "" {
		return fmt.Errorf("the server's control for %s (%s) has no replyTo", what, target.MRID)
	}
	status := uint8(2) // event started
	resp := sep2.DERControlResponse{Response: sep2.Response{
		CreatedDateTime: time.Now().Unix(),
		EndDeviceLFDI:   env.c.LFDI(),
		Status:          &status,
		Subject:         target.MRID,
	}}
	res.Request = map[string]any{"method": "POST", "path": target.ReplyTo}
	if err := env.c.PostResponse(ctx, target.ReplyTo, resp); err != nil {
		return err
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"subject": target.MRID, "status": status}}
	return nil
}

func opLogEvent(ctx context.Context, env *opEnv, res *result) error {
	ed, _, err := ownEndDevice(ctx, env.c)
	if err != nil {
		return err
	}
	if ed.LogEventListLink == nil {
		return errors.New("the EndDevice has no LogEventListLink")
	}
	le := env.f.LogEvent
	evt := sep2.LogEvent{
		CreatedDateTime: time.Now().Unix(),
		Details:         le.Details,
		FunctionSet:     le.FunctionSet,
		LogEventCode:    le.LogEventCode,
		LogEventID:      le.LogEventID,
		LogEventPEN:     le.LogEventPEN,
		ProfileID:       le.ProfileID,
	}
	res.Request = map[string]any{"method": "POST", "path": ed.LogEventListLink.Href}
	loc, err := env.c.PostLogEvent(ctx, ed.LogEventListLink.Href, evt)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}

// derCapability and derSettings are the upstream's sep2.DERCapability and
// sep2.DERSettings with what they lack: setGradW, a required element of
// DERSettings, and the CSIP-AUS extensions doeModesSupported and
// doeModesEnabled, in the CSIP-AUS namespace as Envoy (envoy_schema 2.x)
// reads it. Elements in sep.xsd order, extensions last.
type derCapability struct {
	XMLName        xml.Name          `xml:"urn:ieee:std:2030.5:ns DERCapability"`
	ModesSupported *sep2.HexBinary32 `xml:"modesSupported"`
	RTGMaxW        *sep2.ActivePower `xml:"rtgMaxW"`
	Type           uint8             `xml:"type"`
	DOEModes       *hexByte          `xml:"https://csipaus.org/ns/v1.3 doeModesSupported,omitempty"`
}

type derSettings struct {
	XMLName      xml.Name          `xml:"urn:ieee:std:2030.5:ns DERSettings"`
	ModesEnabled *sep2.HexBinary32 `xml:"modesEnabled,omitempty"`
	SetGradW     uint16            `xml:"setGradW"`
	SetMaxW      *sep2.ActivePower `xml:"setMaxW"`
	UpdatedTime  int64             `xml:"updatedTime"`
	DOEModes     *hexByte          `xml:"https://csipaus.org/ns/v1.3 doeModesEnabled,omitempty"`
}

// hexByte is a HexBinary8 as two upper-case hex digits.
type hexByte string

func optionalHexByte(s string) (*hexByte, error) {
	if s == "" {
		return nil, nil
	}
	if b, err := hex.DecodeString(s); err != nil || len(b) != 1 {
		return nil, codedError{exitFixture, fmt.Errorf("fixture: %q is not 2 hexadecimal digits", s)}
	}
	h := hexByte(strings.ToUpper(s))
	return &h, nil
}

func hex32(s string) (*sep2.HexBinary32, error) {
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return nil, codedError{exitFixture, fmt.Errorf("fixture: %q is not 8 hexadecimal digits", s)}
	}
	h := sep2.HexBinary32(v)
	return &h, nil
}

func activePower(p ActivePower) *sep2.ActivePower {
	return &sep2.ActivePower{Value: int16(p.Value), Multiplier: p.Multiplier}
}

func opPutDER(ctx context.Context, env *opEnv, res *result) error {
	c, d := env.c, env.f.DER
	ed, _, err := ownEndDevice(ctx, c)
	if err != nil {
		return err
	}
	if ed.DERListLink == nil {
		return errors.New("the EndDevice has no DERListLink")
	}
	res.Request = map[string]any{"method": "GET", "path": ed.DERListLink.Href}
	var list sep2.DERList
	if _, err := c.Get(ctx, guard.KindEndDeviceRead, ed.DERListLink.Href, &list); err != nil {
		return fmt.Errorf("GET DERList: %w", err)
	}
	if len(list.DER) == 0 {
		return fmt.Errorf("the DERList %s is empty", ed.DERListLink.Href)
	}
	der := list.DER[0]
	now := time.Now().Unix()
	modes, err := hex32(d.Capability.ModesSupported)
	if err != nil {
		return err
	}
	enabled, err := hex32(d.Settings.ModesEnabled)
	if err != nil {
		return err
	}
	gen, err := hex.DecodeString(d.Status.GenConnectStatus)
	if err != nil || len(gen) != 1 {
		return codedError{exitFixture, fmt.Errorf("fixture: genConnectStatus %q is not 2 hexadecimal digits", d.Status.GenConnectStatus)}
	}
	doeSupported, err := optionalHexByte(d.Capability.DOEModes)
	if err != nil {
		return err
	}
	doeEnabled, err := optionalHexByte(d.Settings.DOEModes)
	if err != nil {
		return err
	}
	availability := d.Availability.AvailabilityDuration
	puts := []struct {
		name string
		link *sep2.Link
		do   func(href string) error
	}{
		{"DERCapability", der.DERCapabilityLink, func(href string) error {
			_, err := c.Put(ctx, guard.KindDERResourceWrite, href, &derCapability{
				ModesSupported: modes, RTGMaxW: activePower(d.Capability.RtgMaxW), Type: d.Capability.Type, DOEModes: doeSupported,
			})
			return err
		}},
		{"DERSettings", der.DERSettingsLink, func(href string) error {
			_, err := c.Put(ctx, guard.KindDERResourceWrite, href, &derSettings{
				ModesEnabled: enabled, SetGradW: d.Settings.SetGradW, SetMaxW: activePower(d.Settings.SetMaxW), UpdatedTime: now,
				DOEModes: doeEnabled,
			})
			return err
		}},
		{"DERStatus", der.DERStatusLink, func(href string) error {
			return c.PutDERStatus(ctx, href, sep2.DERStatus{
				GenConnectStatus:      &sep2.ConnectStatusType{DateTime: now, Value: sep2.HexBinary8(gen[0])},
				OperationalModeStatus: &sep2.OperationalModeStatusType{DateTime: now, Value: d.Status.OperationalModeStatus},
				ReadingTime:           now,
			})
		}},
		{"DERAvailability", der.DERAvailabilityLink, func(href string) error {
			return c.PutDERAvailability(ctx, href, sep2.DERAvailability{
				AvailabilityDuration: &availability, ReadingTime: now, StatWAvail: activePower(d.Availability.StatWAvail),
			})
		}},
	}
	done := []string{}
	for _, p := range puts {
		if p.link == nil || p.link.Href == "" {
			return fmt.Errorf("the DER %s has no %sLink", der.Href, p.name)
		}
		res.Request = map[string]any{"method": "PUT", "path": p.link.Href}
		if err := p.do(p.link.Href); err != nil {
			return fmt.Errorf("PUT %s %s: %w", p.name, p.link.Href, err)
		}
		done = append(done, p.link.Href)
	}
	res.Response = map[string]any{"status": 204, "body": map[string]any{"der": der.Href, "put": done}}
	return nil
}

func opFlowReservation(ctx context.Context, env *opEnv, res *result) error {
	ed, _, err := ownEndDevice(ctx, env.c)
	if err != nil {
		return err
	}
	if ed.FlowReservationRequestListLink == nil {
		return errors.New("the EndDevice has no FlowReservationRequestListLink")
	}
	// The upstream lets only its aggregator role post flow reservations
	// (internal/inverter/guard/guard.go aggregatorSelfKinds).
	cfg := env.cfg
	cfg.ClientRole = string(guard.RoleAggregator)
	agg, err := inverter.NewSEP2Client(cfg)
	if err != nil {
		return err
	}
	fr := env.f.FlowRsv
	now := time.Now().Unix()
	req := sep2.FlowReservationRequest{
		MRID:              fr.MRID,
		Description:       fr.Description,
		CreationTime:      now,
		DurationRequested: fr.DurationRequested,
		EnergyRequested:   &sep2.SignedRealEnergy{Value: fr.EnergyRequested.Value, Multiplier: fr.EnergyRequested.Multiplier},
		IntervalRequested: &sep2.DateTimeInterval{Start: now + int64(fr.Start), Duration: fr.Duration},
		PowerRequested:    activePower(fr.PowerRequested),
		RequestStatus:     sep2.RequestStatus{DateTime: now, RequestStatus: 0},
	}
	href := ed.FlowReservationRequestListLink.Href
	res.Request = map[string]any{"method": "POST", "path": href}
	loc, err := agg.PostFlowReservationRequest(ctx, href, req)
	if err != nil {
		return err
	}
	res.Response = map[string]any{"status": 201, "body": map[string]any{"location": loc}}
	return nil
}

func opSubscribe(ctx context.Context, env *opEnv, res *result) error {
	c := env.c
	ed, _, err := ownEndDevice(ctx, c)
	if err != nil {
		return err
	}
	if ed.SubscriptionListLink == nil {
		return errors.New("the EndDevice has no SubscriptionListLink")
	}
	if ed.FunctionSetAssignmentsListLink == nil {
		return errors.New("the EndDevice has no FunctionSetAssignmentsListLink")
	}
	fsas, _, err := c.GetFSAList(ctx, ed.FunctionSetAssignmentsListLink.Href)
	if err != nil {
		return err
	}
	if len(fsas.FunctionSetAssignments) == 0 || fsas.FunctionSetAssignments[0].DERProgramListLink == nil {
		return errors.New("the device's first FunctionSetAssignments has no DERProgramListLink")
	}
	subscribed := fsas.FunctionSetAssignments[0].DERProgramListLink.Href

	// The listener: notify.crt and notify.key when the test gives them,
	// else the device certificate. The upstream receiver is TLS 1.2 with
	// CCM_8 only and asks the server for a certificate.
	cert, key := filepath.Join(env.tlsDir, "notify.crt"), filepath.Join(env.tlsDir, "notify.key")
	if _, err := os.Stat(cert); err != nil {
		cert, key = env.cfg.CertFile, env.cfg.KeyFile
	}
	notifications := make(chan sep2.Notification, 8)
	rcv, err := inverter.NewNotifyReceiver(inverter.NotifyReceiverConfig{
		CertFile: cert, KeyFile: key, CAFile: env.cfg.CAFile, ListenAddr: env.notifyListen,
		Dispatcher: func(_ context.Context, n sep2.Notification) {
			select {
			case notifications <- n:
			default:
			}
		},
	})
	if err != nil {
		return codedError{exitFixture, fmt.Errorf("notification listener: %w", err)}
	}
	if err := rcv.Start(); err != nil {
		return fmt.Errorf("notification listener: %w", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = rcv.Stop(stopCtx)
	}()

	uri := "https://" + env.notifyHost + "/notify"
	sub := sep2.Subscription{SubscribedResource: subscribed, NotificationURI: uri, Encoding: sep2.EncodingXML, Limit: 1}
	res.Request = map[string]any{"method": "POST", "path": ed.SubscriptionListLink.Href}
	loc, _, err := c.Post(ctx, guard.KindSubscriptionPost, ed.SubscriptionListLink.Href, &sub)
	if err != nil {
		return fmt.Errorf("POST Subscription of %s: %w", subscribed, err)
	}
	body := map[string]any{"location": loc, "subscribedResource": subscribed, "notificationURI": uri}
	res.Response = map[string]any{"status": 201, "body": body}
	select {
	case n := <-notifications:
		body["notification"] = n
		return nil
	case <-ctx.Done():
		return fmt.Errorf("no Notification arrived at %s: %w", uri, ctx.Err())
	}
}
