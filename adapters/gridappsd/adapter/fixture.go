// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Fixture is the part of schemas/fixture.schema.json the adapter uses. The
// repository validates fixtures against the schema; the adapter checks what
// it relies on and exits 4 on a violation.
type Fixture struct {
	SchemaVersion string            `json:"schemaVersion"`
	Name          string            `json:"name"`
	Device        Device            `json:"device"`
	DERPrograms   []DERProgram      `json:"derPrograms"`
	Report        Report            `json:"report"`
	Tariffs       []json.RawMessage `json:"tariffs"`
	Messages      []json.RawMessage `json:"messages"`
	DER           *DERFixture       `json:"der"`
	LogEvent      *LogEventFixture  `json:"logEvent"`
	FlowRsv       *FlowRsvFixture   `json:"flowReservation"`
}

// ActivePower is value x 10^multiplier W (or Wh for energy).
type ActivePower struct {
	Value      int64 `json:"value"`
	Multiplier int8  `json:"multiplier"`
}

// DERFixture is what put-der PUTs.
type DERFixture struct {
	Capability struct {
		ModesSupported string      `json:"modesSupported"`
		RtgMaxW        ActivePower `json:"rtgMaxW"`
		Type           uint8       `json:"type"`
		DOEModes       string      `json:"doeModesSupported"`
	} `json:"capability"`
	Settings struct {
		ModesEnabled string      `json:"modesEnabled"`
		SetMaxW      ActivePower `json:"setMaxW"`
		SetGradW     uint16      `json:"setGradW"`
		DOEModes     string      `json:"doeModesEnabled"`
	} `json:"settings"`
	Status struct {
		GenConnectStatus      string `json:"genConnectStatus"`
		OperationalModeStatus uint8  `json:"operationalModeStatus"`
	} `json:"status"`
	Availability struct {
		AvailabilityDuration uint32      `json:"availabilityDuration"`
		StatWAvail           ActivePower `json:"statWAvail"`
	} `json:"availability"`
}

// LogEventFixture is the LogEvent log-event POSTs.
type LogEventFixture struct {
	LogEventCode uint8  `json:"logEventCode"`
	LogEventID   uint16 `json:"logEventID"`
	LogEventPEN  uint32 `json:"logEventPEN"`
	FunctionSet  uint8  `json:"functionSet"`
	ProfileID    uint8  `json:"profileID"`
	Details      string `json:"details"`
}

// FlowRsvFixture is the FlowReservationRequest flow-reservation POSTs.
type FlowRsvFixture struct {
	MRID              string      `json:"mRID"`
	Description       string      `json:"description"`
	EnergyRequested   ActivePower `json:"energyRequested"`
	PowerRequested    ActivePower `json:"powerRequested"`
	Start             uint32      `json:"start"`
	Duration          uint32      `json:"duration"`
	DurationRequested *uint16     `json:"durationRequested"`
}

// Device is the device of the scenario.
type Device struct {
	Serial         string `json:"serial"`
	PIN            uint32 `json:"pin"`
	DeviceCategory string `json:"deviceCategory"`
}

// DERProgram is a program with its default control and controls.
type DERProgram struct {
	MRID           string    `json:"mRID"`
	Description    string    `json:"description"`
	Primacy        uint8     `json:"primacy"`
	DefaultControl *Control  `json:"defaultControl"`
	Controls       []Control `json:"controls"`
	Curves         []Curve   `json:"curves"`
}

// Curve is a DERCurve of a program.
type Curve struct {
	MRID        string `json:"mRID"`
	Description string `json:"description"`
	CurveType   uint8  `json:"curveType"`
	XMultiplier int8   `json:"xMultiplier"`
	YMultiplier int8   `json:"yMultiplier"`
	YRefType    uint8  `json:"yRefType"`
	Points      []struct {
		X int32 `json:"x"`
		Y int32 `json:"y"`
	} `json:"points"`
}

// Control is a DERControl or DefaultDERControl. Base holds DERControlBase
// elements by their IEEE 2030.5 names.
type Control struct {
	MRID        string                     `json:"mRID"`
	Description string                     `json:"description"`
	Base        map[string]json.RawMessage `json:"base"`
	Duration    uint32                     `json:"duration"`
	Start       uint32                     `json:"start"` // seconds after the server starts
}

// Report is the metering a client reports.
type Report struct {
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
}

var mridPattern = regexp.MustCompile(`^[0-9A-F]{32}$`)

func loadFixture(path string) (*Fixture, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, codedError{exitFixture, fmt.Errorf("fixture: %w", err)}
	}
	var f Fixture
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, nil, codedError{exitFixture, fmt.Errorf("fixture %s: %w", path, err)}
	}
	if f.SchemaVersion != "1.0" || f.Name == "" || f.Device.Serial == "" || len(f.DERPrograms) == 0 ||
		len(f.Report.MeterReadings) == 0 || !mridPattern.MatchString(f.Report.MirrorUsagePoint.MRID) {
		return nil, nil, codedError{exitFixture, fmt.Errorf("fixture %s is not a valid fixture", path)}
	}
	for _, p := range f.DERPrograms {
		if !mridPattern.MatchString(p.MRID) {
			return nil, nil, codedError{exitFixture, fmt.Errorf("fixture %s: program %q has no valid mRID", path, p.Description)}
		}
	}
	return &f, data, nil
}

func printFixture(args []string) error {
	if len(args) > 1 {
		return usagef("print-fixture takes at most one name")
	}
	name := "baseline"
	if len(args) == 1 {
		name = args[0]
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(name) {
		return codedError{exitFailed, fmt.Errorf("no fixture named %q", name)}
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(defaultFixture)), name, "fixture.json"))
	if err != nil {
		return codedError{exitFailed, fmt.Errorf("no fixture named %q", name)}
	}
	_, err = os.Stdout.Write(data)
	return err
}

func printCapabilities(args []string) error {
	if len(args) > 0 {
		return usagef("print-capabilities takes no arguments")
	}
	version := map[string]any{
		"client": map[string]any{
			"supported":  true,
			"operations": clientOperations,
			"fixtures":   clientFixtures,
		},
		"server": map[string]any{
			"supported":  true,
			"operations": serverOperations,
			"fixtures":   serverFixtures,
			"calls":      []string{"issue-control"},
			"events":     []string{"device-registered", "subscription-created"},
		},
		"features": map[string]bool{
			"mTLS":            true,
			"csip":            true,
			"csipAus":         false,
			"subscription":    true,
			"defaultControl":  true,
			"curves":          false,
			"pricing":         false,
			"messaging":       false,
			"flowReservation": true,
		},
		"notes": capabilityNotes,
	}
	doc := map[string]any{
		"schemaVersion":   "1.0",
		"adapter":         adapterName,
		"adapterVersion":  adapterVersion,
		"contractVersion": contractVersion,
		"upstream": map[string]any{
			"name":              "GridAPPS-D IEEE 2030.5 server and client",
			"repository":        "https://github.com/GRIDAPPSD/ieee-2030_5-server-go",
			"version":           serverVersion,
			"revision":          serverRevision,
			"license":           "LicenseRef-Battelle-BSD",
			"language":          "Go",
			"independentEngine": true,
			"client": map[string]any{
				"repository": "https://github.com/GRIDAPPSD/ieee-2030_5-client-go",
				"version":    clientVersion,
				"revision":   clientRevision,
			},
		},
		"tls": map[string]any{
			"versions":     []string{"1.2"},
			"cipherSuites": []string{"TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8"},
		},
		"versions": map[string]any{"2018": version, "2023": version},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}
