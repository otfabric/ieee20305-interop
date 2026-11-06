// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
)

// What the adapter declares: what go-ieee20305's public API offers and the
// self-tests (scripts/smoke.sh, scripts/interop.sh) exercise. Both roles
// take every operation of the contract; CSIP-AUS profiles IEEE 2030.5-2018
// only.
var (
	allOperations = []string{"register", "discover", "poll-controls", "report", "subscribe", "respond", "log-event",
		"put-der", "flow-reservation", "poll-pricing"}
	fixtures2018 = []string{"baseline", "csip", "csip-aus"}
	fixtures2023 = []string{"baseline", "csip"}
)

// The cipher suites of both roles, through the tlsccm module: CCM_8, which
// IEEE 2030.5 mandates, then AES-128-GCM for peers without CCM_8.
var cipherSuites = []string{"TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8", "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256"}

var capabilityNotes = []string{
	"OT Fabric's own stack (go-ieee20305), not an independent implementation: results against it are no independent evidence for go-ieee20305.",
	"TLS: TLS 1.2 with TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 and TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, in both roles, through the tlsccm module of go-ieee20305.",
	"Server: the library's server package with its in-memory store. Devices are provisioned by LFDI from the certificates of --tls-dir/devices with the fixture's PIN; each gets a DER, one FunctionSetAssignments with the fixture's programs, default controls and controls (which start at server start plus their start, and ask for responses to a ResponseSet), and tariffs. CSIP-AUS fixtures are served with the csipaus profile (csipserver.Configure).",
	"Server: in-band registration is on (InBandRegistration): an unprovisioned device may post its EndDevice (201, device-registered) but gets no Registration. device-registered and subscription-created are written by the adapter from the server's 201 answers.",
	"Server: notifications are sent with server.NewNotificationClient (the server certificate, verifying the listener against --tls-dir/ca.crt), to any notificationURI: the default policy refuses private addresses, which every Docker network has.",
	"Server: call issue-control creates the DERControl in the program's DERControlList and then writes the program again, so that the subscribers of its DERProgramList are notified: the library notifies a list's subscribers of changes of its members, and a new control changes the program's DERControlList, not the DERProgramList.",
	"Client: the library's client package; poll-controls, respond and poll-pricing walk the links from the device's FunctionSetAssignments. CSIP-AUS fixtures use the csipaus codec. subscribe listens with crypto/tls on notify.crt (or the device certificate), verifying a client certificate when the server presents one.",
	"Curves and messages are not declared: the library models them, but this adapter neither seeds nor checks them, and no baked fixture has them. A fixture with curves or messages is exit 5.",
}

func printCapabilities(args []string) error {
	if len(args) > 0 {
		return usagef("print-capabilities takes no arguments")
	}
	version := func(fixtures []string, aus bool) map[string]any {
		return map[string]any{
			"client": map[string]any{
				"supported":  true,
				"operations": allOperations,
				"fixtures":   fixtures,
			},
			"server": map[string]any{
				"supported":  true,
				"operations": allOperations,
				"fixtures":   fixtures,
				"calls":      []string{"issue-control"},
				"events":     []string{"device-registered", "subscription-created"},
			},
			"features": map[string]bool{
				"mTLS":            true,
				"csip":            true,
				"csipAus":         aus,
				"subscription":    true,
				"defaultControl":  true,
				"curves":          false,
				"pricing":         true,
				"messaging":       false,
				"flowReservation": true,
			},
			"notes": capabilityNotes,
		}
	}
	doc := map[string]any{
		"schemaVersion":   "1.0",
		"adapter":         adapterName,
		"adapterVersion":  adapterVersion,
		"contractVersion": contractVersion,
		"upstream": map[string]any{
			"name":              "go-ieee20305",
			"repository":        "https://github.com/otfabric/go-ieee20305",
			"version":           upstreamVersion,
			"revision":          upstreamRevision,
			"license":           "MIT",
			"language":          "Go",
			"independentEngine": false,
		},
		"tls": map[string]any{
			"versions":     []string{"1.2"},
			"cipherSuites": cipherSuites,
		},
		"versions": map[string]any{"2018": version(fixtures2018, true), "2023": version(fixtures2023, false)},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = stdout.Write(append(b, '\n'))
	return err
}
