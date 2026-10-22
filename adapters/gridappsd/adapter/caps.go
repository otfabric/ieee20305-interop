// SPDX-License-Identifier: MIT

package main

// What the adapter declares, for both IEEE 2030.5 editions: only what the
// self-tests (scripts/smoke.sh, scripts/interop.sh) exercise and pass.
var (
	serverOperations = []string{"register", "discover", "poll-controls", "report", "subscribe", "respond", "log-event", "flow-reservation"}
	serverFixtures   = []string{"baseline", "csip"}
	clientOperations = []string{"register", "discover", "poll-controls", "report", "respond", "log-event", "put-der", "flow-reservation"}
	clientFixtures   = []string{"baseline", "csip"}
)

const (
	serverTree = "https://github.com/GRIDAPPSD/ieee-2030_5-server-go/blob/d7a4a17042af7506610a973c6be8a5466db34053/"
	clientTree = "https://github.com/GRIDAPPSD/ieee-2030_5-client-go/blob/66570b94ea104b46eb12b48ef2451b3454050176/"
	coreTree   = "https://github.com/GRIDAPPSD/ieee-2030_5-core-go/blob/v0.27.0/"
)

var capabilityNotes = []string{
	"TLS: TLS 1.2 with TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 only, in both roles, through the GridAPPS-D fork of crypto/tls; a peer that does not offer CCM_8 cannot connect.",
	"Server: devices are provisioned by LFDI from the certificates of --tls-dir/devices; each gets one FunctionSetAssignments, under which the admin API creates the fixture's programs (description, primacy, mRID), default controls and controls. The server picks the hrefs and the controls' mRIDs.",
	"Server: patched (adapters/gridappsd/patches/0001): v0.18.0 mounts GET and POST /edev/{id}/sub but never sets EndDevice.SubscriptionListLink, so no client could find where to subscribe (" + serverTree + "pkg/sep2srv/assembly/assembly.go#L830, " + serverTree + "pkg/store/memory/logeventbinding.go#L113).",
	"Server: a fixture with curves is refused (exit 5): sep2server stores curves only from its boot fixture and mounts only GET /dc, so a control's curve link /dc/{id} is not served (" + serverTree + "pkg/sep2srv/assembly/assembly.go#L1049, " + serverTree + "internal/dercontrol/dercurve/dercurve.go#L21), and an admin-created program has no DERCurveListLink.",
	"Server: the fixture csip-aus is refused (exit 5): the admin control catalog has no CSIP-AUS limits (" + serverTree + "internal/dercontrol/catalog.go#L22). Tariffs and messages are refused too: there is no pricing function set and no admin route to seed MessagingPrograms.",
	"Server: a program without a defaultControl is created with one and then cleared (the admin API requires it at creation, " + serverTree + "internal/handler/admin_derprogram.go#L229). A DefaultDERControl is served without the fixture's description: the admin API takes none.",
	"Server: put-der is not served: sep2server stamps no DERListLink on an EndDevice, and a DER exists only after the device PUTs /edev/{id}/der/{derId} itself (" + serverTree + "pkg/sep2srv/assembly/assembly.go#L964).",
	"Server: device-registered and subscription-created come from sep2server's traffic capture (SEP2_TRAFFIC_CAPTURE, admin GET /api/traffic/stream): sep2server logs neither. An unprovisioned device's POST /edev is answered 201 (an EndDevice without Registration).",
	"Server: call issue-control issues the control through the admin API (POST /api/der/controls): functions opModConnect, opModEnergize, opModMaxLimW and opModFixedW, duration 60 s to 24 h, start at most 7 days ahead. sep2server notifies the subscribers of the program's DERProgramList and DERControlList.",
	"Server: notifications are delivered with Go's standard HTTP client, without a client certificate and without CCM_8 (" + serverTree + "pkg/sep2srv/handlers/subscription/destination.go#L290); the adapter makes the test CA (--tls-dir/ca.crt) its trusted root (SSL_CERT_FILE).",
	"Client: verifies the server's certificate chain, not its host name: IEEE 2030.5 server certificates need not carry one (" + clientTree + "internal/inverter/client.go, GRIDAPPSD/ieee-2030_5-server-go#32).",
	"Client: subscribe is not declared: the upstream notification listener accepts only CCM_8 and requires a client certificate (" + clientTree + "internal/inverter/notify.go#L130, core sep2tls.NewCCMServerConfig), and sep2server delivers with neither, so no Notification arrives (sep2server: \"remote error: tls: handshake failure\"). It works against a server that delivers with CCM_8 and a certificate.",
	"Client: put-der PUTs DERCapability and DERSettings as adapter types sent through the upstream client (SEP2Client.Put): sep2.DERSettings has no setGradW, a required element (" + coreTree + "pkg/sep2/der.go#L237), and the core types have no CSIP-AUS elements, so the fixture's doeModesSupported and doeModesEnabled are added in the namespace https://csipaus.org/ns/v1.3. DERStatus and DERAvailability are the upstream's types. Against sep2server it fails: no DERListLink (see the server note).",
	"Client: flow-reservation posts through a second client of the upstream's aggregator role: the der role may not post flow reservations (" + clientTree + "internal/inverter/guard/guard.go#L194).",
	"Client: poll-pricing is unsupported (exit 5): the upstream and its core types have no pricing function set. Fixtures with CSIP-AUS limits are unsupported for poll-controls and respond: sep2.DERControlBase has no CSIP-AUS elements. poll-controls checks default controls and curves (through controls' curve links and the DERCurveListLink); no server adapter serves curves, so curves are not declared.",
}
