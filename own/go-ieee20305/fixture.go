// SPDX-License-Identifier: MIT

package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/client"
	"github.com/otfabric/go-ieee20305/csipaus"
)

// power is an ActivePower (or an energy) of a fixture: value x 10^multiplier.
type power struct {
	Value      int64 `json:"value"`
	Multiplier int8  `json:"multiplier"`
}

func (p *power) active() *ieee20305.ActivePower {
	if p == nil {
		return nil
	}
	return &ieee20305.ActivePower{Value: ieee20305.Int16(p.Value), Multiplier: ieee20305.PowerOfTenMultiplierType(p.Multiplier)}
}

// control is a control or default control of a fixture.
type control struct {
	MRID        string         `json:"mRID"`
	Description string         `json:"description"`
	Base        map[string]any `json:"base"`
	Duration    uint32         `json:"duration"`
	Start       uint32         `json:"start"`
}

// derProgram is a DER program of a fixture.
type derProgram struct {
	MRID           string            `json:"mRID"`
	Description    string            `json:"description"`
	Primacy        uint8             `json:"primacy"`
	DefaultControl *control          `json:"defaultControl"`
	Controls       []control         `json:"controls"`
	Curves         []json.RawMessage `json:"curves"`
}

// fixture is the part of an ieee20305-interop fixture (fixtures/schema/ieee20305-fixture.schema.json)
// the adapter uses. The repository validates fixtures against the schema;
// the adapter checks what it relies on and exits 4 on a violation.
type fixture struct {
	SchemaVersion string `json:"schemaVersion"`
	Name          string `json:"name"`
	Device        struct {
		Serial         string `json:"serial"`
		PIN            uint32 `json:"pin"`
		DeviceCategory string `json:"deviceCategory"`
	} `json:"device"`
	DERPrograms []derProgram `json:"derPrograms"`
	Report      struct {
		MirrorUsagePoint struct {
			MRID                string `json:"mRID"`
			Description         string `json:"description"`
			RoleFlags           string `json:"roleFlags"`
			ServiceCategoryKind uint8  `json:"serviceCategoryKind"`
			Status              uint8  `json:"status"`
		} `json:"mirrorUsagePoint"`
		MeterReadings []struct {
			MRID        string           `json:"mRID"`
			Description string           `json:"description"`
			ReadingType map[string]int64 `json:"readingType"`
			Value       int64            `json:"value"`
		} `json:"meterReadings"`
	} `json:"report"`
	DER *struct {
		Capability struct {
			ModesSupported    string `json:"modesSupported"`
			RtgMaxW           *power `json:"rtgMaxW"`
			Type              uint8  `json:"type"`
			DoeModesSupported string `json:"doeModesSupported"`
		} `json:"capability"`
		Settings struct {
			ModesEnabled    string `json:"modesEnabled"`
			SetMaxW         *power `json:"setMaxW"`
			SetGradW        uint16 `json:"setGradW"`
			DoeModesEnabled string `json:"doeModesEnabled"`
		} `json:"settings"`
		Status struct {
			GenConnectStatus      string `json:"genConnectStatus"`
			OperationalModeStatus uint8  `json:"operationalModeStatus"`
		} `json:"status"`
		Availability struct {
			AvailabilityDuration uint32 `json:"availabilityDuration"`
			StatWAvail           *power `json:"statWAvail"`
		} `json:"availability"`
	} `json:"der"`
	LogEvent *struct {
		LogEventCode uint8  `json:"logEventCode"`
		LogEventID   uint16 `json:"logEventID"`
		LogEventPEN  uint32 `json:"logEventPEN"`
		FunctionSet  uint8  `json:"functionSet"`
		ProfileID    uint8  `json:"profileID"`
		Details      string `json:"details"`
	} `json:"logEvent"`
	FlowReservation *struct {
		MRID              string `json:"mRID"`
		Description       string `json:"description"`
		EnergyRequested   power  `json:"energyRequested"`
		PowerRequested    power  `json:"powerRequested"`
		Start             uint32 `json:"start"`
		Duration          uint32 `json:"duration"`
		DurationRequested uint16 `json:"durationRequested"`
	} `json:"flowReservation"`
	Tariffs []struct {
		MRID                      string `json:"mRID"`
		Description               string `json:"description"`
		Currency                  uint16 `json:"currency"`
		PricePowerOfTenMultiplier int8   `json:"pricePowerOfTenMultiplier"`
		Primacy                   uint8  `json:"primacy"`
		RateCode                  string `json:"rateCode"`
		RateComponents            []struct {
			MRID                string `json:"mRID"`
			Description         string `json:"description"`
			RoleFlags           string `json:"roleFlags"`
			FlowDirection       uint8  `json:"flowDirection"`
			TimeTariffIntervals []struct {
				MRID        string `json:"mRID"`
				Description string `json:"description"`
				Start       uint32 `json:"start"`
				Duration    uint32 `json:"duration"`
				Price       int32  `json:"price"`
			} `json:"timeTariffIntervals"`
		} `json:"rateComponents"`
	} `json:"tariffs"`
	Messages []json.RawMessage `json:"messages"`
}

var mridPattern = regexp.MustCompile(`^[0-9A-Fa-f]{32}$`)

// loadFixture reads and checks a fixture: exit 4 when it is not one.
func loadFixture(path string) (*fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, codedError{exitFixture, fmt.Errorf("fixture: %w", err)}
	}
	var f fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, codedError{exitFixture, fmt.Errorf("fixture %s: %w", path, err)}
	}
	if err := f.check(); err != nil {
		return nil, codedError{exitFixture, fmt.Errorf("fixture %s: %w", path, err)}
	}
	return &f, nil
}

func (f *fixture) check() error {
	if f.SchemaVersion != "1.0" || f.Name == "" || f.Device.Serial == "" || len(f.DERPrograms) == 0 {
		return errors.New("not a fixture: schemaVersion, name, device.serial or derPrograms is missing")
	}
	hexes := []string{f.Report.MirrorUsagePoint.MRID}
	for _, p := range f.DERPrograms {
		hexes = append(hexes, p.MRID)
		for _, c := range append(p.Controls, deref(p.DefaultControl)...) {
			hexes = append(hexes, c.MRID)
			if _, err := base(c.Base); err != nil {
				return fmt.Errorf("program %q: %w", p.Description, err)
			}
		}
	}
	for _, r := range f.Report.MeterReadings {
		hexes = append(hexes, r.MRID)
	}
	for _, h := range hexes {
		if !mridPattern.MatchString(h) {
			return fmt.Errorf("%q is not an mRID of 32 hexadecimal digits", h)
		}
	}
	if _, err := hex.DecodeString(f.Report.MirrorUsagePoint.RoleFlags); err != nil {
		return fmt.Errorf("report.mirrorUsagePoint.roleFlags: %w", err)
	}
	if f.Device.DeviceCategory != "" {
		if _, err := hex.DecodeString(f.Device.DeviceCategory); err != nil {
			return fmt.Errorf("device.deviceCategory: %w", err)
		}
	}
	return nil
}

// csipAUS reports whether a fixture is of CSIP-AUS: its controls set site
// limits.
func (f *fixture) csipAUS() bool {
	for _, p := range f.DERPrograms {
		for _, c := range append(p.Controls, deref(p.DefaultControl)...) {
			for k := range c.Base {
				if strings.HasPrefix(k, "opModExpLim") || strings.HasPrefix(k, "opModImpLim") ||
					strings.HasPrefix(k, "opModGenLim") || strings.HasPrefix(k, "opModLoadLim") {
					return true
				}
			}
		}
	}
	return false
}

// unsupported says what of a fixture the adapter cannot seed or check, or
// "": curves and messages (not wired, see capabilityNotes), and CSIP-AUS in
// another edition than 2018.
func (f *fixture) unsupported(edition ieee20305.Edition) string {
	for _, p := range f.DERPrograms {
		if len(p.Curves) > 0 {
			return fmt.Sprintf("the fixture %s has curves (program %q), which this adapter does not seed or check", f.Name, p.Description)
		}
	}
	if len(f.Messages) > 0 {
		return fmt.Sprintf("the fixture %s has messages, which this adapter does not seed or check", f.Name)
	}
	if f.csipAUS() && edition != ieee20305.Edition2018 {
		return fmt.Sprintf("the fixture %s is of CSIP-AUS, which profiles IEEE 2030.5-2018 only", f.Name)
	}
	return ""
}

// codec is the codec of the fixture's documents in edition.
func (f *fixture) codec(edition ieee20305.Edition) client.Codec {
	if f.csipAUS() {
		return csipaus.Codec{}
	}
	return edition.Codec()
}

func deref(c *control) []control {
	if c == nil {
		return nil
	}
	return []control{*c}
}

func unhex(s string) []byte {
	b, _ := hex.DecodeString(strings.TrimSpace(s)) // checked by fixture.check or optional
	return b
}

func str32(s string) *ieee20305.String32 { v := ieee20305.String32(s); return &v }

func describe(s *ieee20305.String32) string {
	if s == nil {
		return ""
	}
	return string(*s)
}

// base returns the DERControlBase of a fixture's base, with the CSIP-AUS
// limits.
func base(m map[string]any) (csipaus.DERControlBase, error) {
	var b csipaus.DERControlBase
	limit := func(k string, v any) (*ieee20305.ActivePower, error) {
		o, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %v is not an ActivePower", k, v)
		}
		value, _ := o["value"].(float64)
		mult, _ := o["multiplier"].(float64)
		return &ieee20305.ActivePower{Value: ieee20305.Int16(value), Multiplier: ieee20305.PowerOfTenMultiplierType(mult)}, nil
	}
	var err error
	for k, v := range m {
		switch k {
		case "opModConnect", "opModEnergize":
			x, ok := v.(bool)
			if !ok {
				return b, fmt.Errorf("%s: %v is not a boolean", k, v)
			}
			if k == "opModConnect" {
				b.OpModConnect = &x
			} else {
				b.OpModEnergize = &x
			}
		case "opModMaxLimW":
			n, ok := v.(float64)
			if !ok {
				return b, fmt.Errorf("%s: %v is not a number", k, v)
			}
			b.OpModMaxLimW = &ieee20305.PerCentControlType{Value: ieee20305.PerCent(n)}
		case "opModFixedW":
			n, ok := v.(float64)
			if !ok {
				return b, fmt.Errorf("%s: %v is not a number", k, v)
			}
			b.OpModFixedW = &ieee20305.SignedPerCentControlType{Value: ieee20305.SignedPerCent(n)}
		case "opModExpLimW":
			b.OpModExpLimW, err = limit(k, v)
		case "opModImpLimW":
			b.OpModImpLimW, err = limit(k, v)
		case "opModGenLimW":
			b.OpModGenLimW, err = limit(k, v)
		case "opModLoadLimW":
			b.OpModLoadLimW, err = limit(k, v)
		default:
			return b, codedError{exitUnsupported, fmt.Errorf("the DERControlBase element %s is not one this adapter knows", k)}
		}
		if err != nil {
			return b, err
		}
	}
	return b, nil
}

// sameBase reports whether b sets exactly the controls of the fixture's
// base, to its values.
func sameBase(b *csipaus.DERControlBase, m map[string]any) bool {
	got := map[string]any{}
	if b.OpModConnect != nil {
		got["opModConnect"] = *b.OpModConnect
	}
	if b.OpModEnergize != nil {
		got["opModEnergize"] = *b.OpModEnergize
	}
	if b.OpModMaxLimW != nil {
		got["opModMaxLimW"] = float64(b.OpModMaxLimW.Value)
	}
	if b.OpModFixedW != nil {
		got["opModFixedW"] = float64(b.OpModFixedW.Value)
	}
	for k, l := range map[string]*ieee20305.ActivePower{"opModExpLimW": b.OpModExpLimW, "opModImpLimW": b.OpModImpLimW,
		"opModGenLimW": b.OpModGenLimW, "opModLoadLimW": b.OpModLoadLimW} {
		if l != nil {
			got[k] = fmt.Sprintf("%d*10^%d", l.Value, l.Multiplier)
		}
	}
	if len(got) != len(m) {
		return false
	}
	for k, v := range m {
		if o, ok := v.(map[string]any); ok {
			value, _ := o["value"].(float64)
			mult, _ := o["multiplier"].(float64)
			v = fmt.Sprintf("%d*10^%d", int64(value), int64(mult))
		}
		if got[k] != v {
			return false
		}
	}
	return true
}

// ders returns the DER resources of a fixture, as put-der puts them: the
// types of CSIP-AUS when it sets their elements.
func (f *fixture) ders() (capability, settings, status, availability any) {
	d := f.DER
	now := ieee20305.TimeType(time.Now().Unix())
	cp := ieee20305.DERCapability{ModesSupported: unhex(d.Capability.ModesSupported), Type: ieee20305.DERType(d.Capability.Type)}
	if d.Capability.RtgMaxW != nil {
		cp.RtgMaxW = *d.Capability.RtgMaxW.active()
	}
	st := ieee20305.DERSettings{SetGradW: ieee20305.UInt16(d.Settings.SetGradW), UpdatedTime: now}
	if d.Settings.ModesEnabled != "" {
		m := ieee20305.DERControlType(unhex(d.Settings.ModesEnabled))
		st.ModesEnabled = &m
	}
	if d.Settings.SetMaxW != nil {
		st.SetMaxW = *d.Settings.SetMaxW.active()
	}
	status = &ieee20305.DERStatus{ReadingTime: now,
		GenConnectStatus:      &ieee20305.ConnectStatusType{DateTime: now, Value: unhex(d.Status.GenConnectStatus)},
		OperationalModeStatus: &ieee20305.OperationalModeStatusType{DateTime: now, Value: ieee20305.UInt8(d.Status.OperationalModeStatus)}}
	dur := ieee20305.UInt32(d.Availability.AvailabilityDuration)
	availability = &ieee20305.DERAvailability{ReadingTime: now, AvailabilityDuration: &dur, StatWAvail: d.Availability.StatWAvail.active()}
	if d.Capability.DoeModesSupported != "" || d.Settings.DoeModesEnabled != "" {
		c := &csipaus.DERCapability{DERCapability: cp, DoeModesSupported: unhex(d.Capability.DoeModesSupported)}
		s := &csipaus.DERSettings{DERSettings: st}
		if d.Settings.DoeModesEnabled != "" {
			e := csipaus.DOEControlType(unhex(d.Settings.DoeModesEnabled))
			s.DoeModesEnabled = &e
		}
		return c, s, status, availability
	}
	return &cp, &st, status, availability
}

// logEvent returns the LogEvent of a fixture.
func (f *fixture) logEvent() *ieee20305.LogEvent {
	l := f.LogEvent
	ev := &ieee20305.LogEvent{CreatedDateTime: ieee20305.TimeType(time.Now().Unix()), LogEventCode: ieee20305.UInt8(l.LogEventCode),
		LogEventID: ieee20305.UInt16(l.LogEventID), LogEventPEN: ieee20305.PENType(l.LogEventPEN),
		FunctionSet: ieee20305.UInt8(l.FunctionSet), ProfileID: ieee20305.UInt8(l.ProfileID)}
	if l.Details != "" {
		ev.Details = str32(l.Details)
	}
	return ev
}

// flowReservation returns the FlowReservationRequest of a fixture; its
// interval starts flowReservation.start seconds from now.
func (f *fixture) flowReservation() *ieee20305.FlowReservationRequest {
	r := f.FlowReservation
	now := time.Now().Unix()
	req := &ieee20305.FlowReservationRequest{CreationTime: ieee20305.TimeType(now),
		EnergyRequested:   ieee20305.SignedRealEnergy{Value: ieee20305.Int48(r.EnergyRequested.Value), Multiplier: ieee20305.PowerOfTenMultiplierType(r.EnergyRequested.Multiplier)},
		PowerRequested:    *r.PowerRequested.active(),
		IntervalRequested: ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now + int64(r.Start)), Duration: ieee20305.UInt32(r.Duration)}}
	req.MRID = unhex(r.MRID)
	if r.DurationRequested > 0 {
		d := ieee20305.UInt16(r.DurationRequested)
		req.DurationRequested = &d
	}
	if r.Description != "" {
		req.Description = str32(r.Description)
	}
	return req
}

// mirrorUsagePoint builds the fixture's MirrorUsagePoint for the device
// id, with its readings taken now.
func (f *fixture) mirrorUsagePoint(id client.Identity) *ieee20305.MirrorUsagePoint {
	m := f.Report.MirrorUsagePoint
	mup := &ieee20305.MirrorUsagePoint{DeviceLFDI: ieee20305.HexBinary160(id.LFDI[:])}
	mup.MRID = unhex(m.MRID)
	mup.Description = str32(m.Description)
	mup.RoleFlags = unhex(m.RoleFlags)
	mup.ServiceCategoryKind = ieee20305.ServiceKind(m.ServiceCategoryKind)
	mup.Status = ieee20305.UInt8(m.Status)
	now := ieee20305.TimeType(time.Now().Unix())
	for _, r := range f.Report.MeterReadings {
		rt := &ieee20305.ReadingType{}
		u8 := func(name string) (uint8, bool) {
			v, ok := r.ReadingType[name]
			return uint8(v), ok
		}
		if v, ok := u8("accumulationBehaviour"); ok {
			x := ieee20305.AccumulationBehaviourType(v)
			rt.AccumulationBehaviour = &x
		}
		if v, ok := u8("commodity"); ok {
			x := ieee20305.CommodityType(v)
			rt.Commodity = &x
		}
		if v, ok := u8("dataQualifier"); ok {
			x := ieee20305.DataQualifierType(v)
			rt.DataQualifier = &x
		}
		if v, ok := u8("flowDirection"); ok {
			x := ieee20305.FlowDirectionType(v)
			rt.FlowDirection = &x
		}
		if v, ok := u8("kind"); ok {
			x := ieee20305.KindType(v)
			rt.Kind = &x
		}
		if v, ok := u8("phase"); ok {
			x := ieee20305.PhaseCode(v)
			rt.Phase = &x
		}
		if v, ok := u8("uom"); ok {
			x := ieee20305.UomType(v)
			rt.Uom = &x
		}
		if v, ok := r.ReadingType["powerOfTenMultiplier"]; ok {
			x := ieee20305.PowerOfTenMultiplierType(v)
			rt.PowerOfTenMultiplier = &x
		}
		value := ieee20305.Int48(r.Value)
		reading := &ieee20305.Reading{}
		reading.Value = &value
		reading.TimePeriod = &ieee20305.DateTimeInterval{Start: now}
		mmr := ieee20305.MirrorMeterReading{ReadingType: rt, Reading: reading, LastUpdateTime: &now}
		mmr.MRID = unhex(r.MRID)
		mmr.Description = str32(r.Description)
		mup.MirrorMeterReading = append(mup.MirrorMeterReading, mmr)
	}
	return mup
}

var fixtureName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func printFixture(args []string) error {
	if len(args) > 1 {
		return usagef("print-fixture takes at most one name")
	}
	name := "baseline"
	if len(args) == 1 {
		name = args[0]
	}
	if !fixtureName.MatchString(name) {
		return codedError{exitFailed, fmt.Errorf("no fixture named %q", name)}
	}
	data, err := os.ReadFile(filepath.Join(fixtureRoot, name, "fixture.json"))
	if err != nil {
		return codedError{exitFailed, fmt.Errorf("no fixture named %q", name)}
	}
	_, err = stdout.Write(data)
	return err
}
