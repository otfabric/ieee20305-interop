// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/security"
)

// The repository's fixtures are not in the build stage, so these tests carry
// their own. scripts/smoke.sh and scripts/interop.sh test against the real ones.
const testFixture = `{
  "schemaVersion": "1.0", "name": "test",
  "device": {"serial": "TEST-0001", "pin": 111115, "deviceCategory": "00020000"},
  "derPrograms": [{
    "mRID": "A0000000000000000000000000000001", "description": "Test program", "primacy": 1,
    "defaultControl": {"mRID": "A0000000000000000000000000000002", "description": "Default", "base": {"opModMaxLimW": 10000}},
    "controls": [
      {"mRID": "A0000000000000000000000000000003", "description": "Now", "base": {"opModConnect": true}, "duration": 600, "start": 0},
      {"mRID": "A0000000000000000000000000000004", "description": "Later", "base": {"opModFixedW": -500}, "duration": 900, "start": 3600}
    ]
  }],
  "report": {
    "mirrorUsagePoint": {"mRID": "B0000000000000000000000000000001", "description": "Site", "roleFlags": "0049", "serviceCategoryKind": 0, "status": 1},
    "meterReadings": [{"mRID": "B0000000000000000000000000000002", "description": "Power", "readingType": {"uom": 38, "kind": 37, "powerOfTenMultiplier": 0}, "value": 4200}]
  },
  "der": {
    "capability": {"modesSupported": "00000001", "rtgMaxW": {"value": 5000, "multiplier": 0}, "type": 4},
    "settings": {"modesEnabled": "00000001", "setMaxW": {"value": 5000, "multiplier": 0}, "setGradW": 100},
    "status": {"genConnectStatus": "01", "operationalModeStatus": 2},
    "availability": {"availabilityDuration": 3600, "statWAvail": {"value": 4000, "multiplier": 0}}
  },
  "logEvent": {"logEventCode": 1, "logEventID": 7, "logEventPEN": 40732, "functionSet": 0, "profileID": 2},
  "flowReservation": {"mRID": "C0000000000000000000000000000001", "description": "Charge",
    "energyRequested": {"value": 10, "multiplier": 3}, "powerRequested": {"value": 7, "multiplier": 3}, "start": 60, "duration": 3600}
}`

// capture runs fn with stdout captured.
var captureMu sync.Mutex

func capture(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	captureMu.Lock()
	defer captureMu.Unlock()
	var buf syncBuffer
	stdoutMu.Lock()
	old := stdout
	stdout = &buf
	stdoutMu.Unlock()
	code := fn()
	stdoutMu.Lock()
	stdout = old
	stdoutMu.Unlock()
	return buf.String(), code
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func writeFixture(t *testing.T, doc string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fixture.json")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"frobnicate"},
		{"server", "--tls-dir", "/tls"},
		{"server", "--version", "2018", "--tls-dir", "/tls", "--frobnicate", "1"},
		{"server", "--version", "2018", "--version", "2018", "--tls-dir", "/tls"},
		{"server", "--version", "9999", "--tls-dir", "/tls"},
		{"server", "--version", "2018"},
		{"server", "--version", "2018", "--tls-dir", "/tls", "--port", "70000"},
		{"client", "register", "--version", "2018", "--tls-dir", "/tls"},
		{"client", "register", "--url", "https://nowhere/dcap", "--tls-dir", "/tls"},
		{"client", "register", "--version", "2018", "--url", "http://nowhere/dcap", "--tls-dir", "/tls"},
		{"client", "frobnicate", "--version", "2018", "--url", "https://nowhere/dcap", "--tls-dir", "/tls"},
		{"client", "register", "--version", "2018", "--url", "https://nowhere/dcap", "--tls-dir", "/tls", "--timeout-ms", "0"},
		{"client", "subscribe", "--version", "2018", "--url", "https://nowhere/dcap", "--tls-dir", "/tls", "--notify-host", "a/b"},
		{"call", "issue-control", "--frobnicate", "1"},
		{"call", "issue-control", "--payload", "{"},
		{"call"},
		{"version", "extra"},
		{"print-capabilities", "extra"},
	} {
		if _, code := capture(t, func() int { return run(args) }); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
	}
}

func TestPrintCapabilities(t *testing.T) {
	out, code := capture(t, func() int { return run([]string{"print-capabilities"}) })
	if code != 0 || strings.Count(out, "\n") != 1 {
		t.Fatalf("exit %d, %q", code, out)
	}
	var doc struct {
		Adapter  string `json:"adapter"`
		Upstream struct {
			IndependentEngine bool `json:"independentEngine"`
		} `json:"upstream"`
		Versions map[string]struct {
			Client struct {
				Operations []string `json:"operations"`
				Fixtures   []string `json:"fixtures"`
			} `json:"client"`
			Features map[string]bool `json:"features"`
		} `json:"versions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Adapter != "otfabric" || doc.Upstream.IndependentEngine {
		t.Errorf("adapter %q, independent %v", doc.Adapter, doc.Upstream.IndependentEngine)
	}
	if len(doc.Versions["2018"].Client.Operations) != 10 || !doc.Versions["2018"].Features["csipAus"] || doc.Versions["2023"].Features["csipAus"] {
		t.Errorf("versions: %+v", doc.Versions)
	}
	for _, fx := range doc.Versions["2023"].Client.Fixtures {
		if fx == "csip-aus" {
			t.Error("2023 declares csip-aus")
		}
	}
}

func TestCallWithoutServer(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "none.sock")
	out, code := capture(t, func() int {
		return run([]string{"call", "issue-control", "--payload", "{}", "--control-socket", sock, "--timeout-ms", "500"})
	})
	if code != exitFailed || !strings.Contains(out, `"not-connected"`) {
		t.Errorf("exit %d, %s", code, out)
	}
	out, code = capture(t, func() int { return run([]string{"call", "cancel-control", "--control-socket", sock}) })
	if code != exitUnsupported || !strings.Contains(out, `"unsupported"`) {
		t.Errorf("exit %d, %s", code, out)
	}
}

func TestFixture(t *testing.T) {
	f, err := loadFixture(writeFixture(t, testFixture))
	if err != nil {
		t.Fatal(err)
	}
	if f.csipAUS() || f.unsupported(ieee20305.Edition2023) != "" {
		t.Errorf("csipAUS %v, unsupported %q", f.csipAUS(), f.unsupported(ieee20305.Edition2023))
	}
	for name, doc := range map[string]string{
		"not JSON":     "{",
		"no programs":  `{"schemaVersion": "1.0", "name": "x", "device": {"serial": "s"}}`,
		"bad mRID":     strings.Replace(testFixture, "A0000000000000000000000000000001", "XYZ", 1),
		"bad element":  strings.Replace(testFixture, `"opModConnect": true`, `"opModConnect": 3`, 1),
		"bad roleFlag": strings.Replace(testFixture, `"0049"`, `"zz"`, 1),
	} {
		if _, err := loadFixture(writeFixture(t, doc)); exitCode(err) != exitFixture {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := loadFixture(filepath.Join(t.TempDir(), "missing.json")); exitCode(err) != exitFixture {
		t.Errorf("missing: %v", err)
	}
	unknown := strings.Replace(testFixture, `"opModConnect": true`, `"opModVoltVar": true`, 1)
	if _, err := loadFixture(writeFixture(t, unknown)); err == nil {
		t.Error("an unknown DERControlBase element was taken")
	}
	curves := strings.Replace(testFixture, `"controls": [`, `"curves": [{"mRID": "D0000000000000000000000000000001"}], "controls": [`, 1)
	f, err = loadFixture(writeFixture(t, curves))
	if err != nil || f.unsupported(ieee20305.Edition2018) == "" {
		t.Errorf("curves: %v, %q", err, f.unsupported(ieee20305.Edition2018))
	}
	aus := strings.Replace(testFixture, `"opModMaxLimW": 10000`, `"opModExpLimW": {"value": 5, "multiplier": 3}`, 1)
	f, err = loadFixture(writeFixture(t, aus))
	if err != nil || !f.csipAUS() || f.unsupported(ieee20305.Edition2018) != "" || f.unsupported(ieee20305.Edition2023) == "" {
		t.Errorf("csip-aus: %v", err)
	}
	if _, ok := f.codec(ieee20305.Edition2018).(csipaus.Codec); !ok {
		t.Errorf("the codec of a CSIP-AUS fixture is %T", f.codec(ieee20305.Edition2018))
	}
}

func TestSameBase(t *testing.T) {
	m := map[string]any{"opModConnect": true, "opModMaxLimW": float64(50), "opModExpLimW": map[string]any{"value": float64(5), "multiplier": float64(3)}}
	b, err := base(m)
	if err != nil {
		t.Fatal(err)
	}
	if !sameBase(&b, m) {
		t.Error("a base differs from itself")
	}
	if sameBase(&b, map[string]any{"opModConnect": true}) || sameBase(&b, map[string]any{"opModConnect": false, "opModMaxLimW": float64(50),
		"opModExpLimW": map[string]any{"value": float64(5), "multiplier": float64(3)}}) {
		t.Error("different bases are the same")
	}
}

func TestRootElement(t *testing.T) {
	for doc, want := range map[string]string{
		`<?xml version="1.0"?><EndDevice xmlns="urn:ieee:std:2030.5:ns"/>`:                         "EndDevice",
		`<Subscription xmlns="urn:ieee:std:2030.5:ns"><subscribedResource>/x</subscribedResource>`: "Subscription",
		`not XML`: "",
	} {
		if got, _ := rootElement([]byte(doc)); got != want {
			t.Errorf("%q: %q, want %q", doc, got, want)
		}
	}
}

func TestPrintFixture(t *testing.T) {
	for _, args := range [][]string{{"print-fixture", "../etc"}, {"print-fixture", "no-such-fixture"}} {
		if _, code := capture(t, func() int { return run(args) }); code != exitFailed {
			t.Errorf("%q: exit %d", args, code)
		}
	}
	if _, code := capture(t, func() int { return run([]string{"print-fixture", "a", "b"}) }); code != exitUsage {
		t.Errorf("two names: exit %d", code)
	}
}

// pkiDir writes the --tls-dir layout of the contract: server/ with
// devices/, client/ and other-ca/.
func pkiDir(t *testing.T, serial string) string {
	t.Helper()
	dir := t.TempDir()
	ca, caKey, err := security.TestCA("test CA")
	if err != nil {
		t.Fatal(err)
	}
	rogue, rogueKey, err := security.TestCA("rogue CA")
	if err != nil {
		t.Fatal(err)
	}
	srv, srvKey, err := security.TestServerCert(ca, caKey, []string{"localhost", "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	hmn := security.HMN{Type: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 40732, 1, 1}, SerialNumber: []byte(serial)}
	dev, devKey, err := security.TestDeviceCertHMN(ca, caKey, hmn)
	if err != nil {
		t.Fatal(err)
	}
	other, otherKey, err := security.TestDeviceCertHMN(rogue, rogueKey, hmn)
	if err != nil {
		t.Fatal(err)
	}
	writeCert := func(p string, c *x509.Certificate) {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeKey := func(p string, k *ecdsa.PrivateKey) {
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCert(filepath.Join(dir, "server/server.crt"), srv)
	writeKey(filepath.Join(dir, "server/server.key"), srvKey)
	writeCert(filepath.Join(dir, "server/devices", serial+".crt"), dev)
	writeCert(filepath.Join(dir, "client/client.crt"), dev)
	writeKey(filepath.Join(dir, "client/client.key"), devKey)
	writeCert(filepath.Join(dir, "client/notify.crt"), srv)
	writeKey(filepath.Join(dir, "client/notify.key"), srvKey)
	writeCert(filepath.Join(dir, "other-ca/client.crt"), other)
	writeKey(filepath.Join(dir, "other-ca/client.key"), otherKey)
	for _, d := range []string{"server", "client", "other-ca"} {
		writeCert(filepath.Join(dir, d, "ca.crt"), ca)
	}
	return dir
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// TestServerAndClient runs the server of the adapter and its client
// against it, in this process, with every client operation.
func TestServerAndClient(t *testing.T) {
	fx := writeFixture(t, testFixture)
	dir := pkiDir(t, "TEST-0001")
	port := freePort(t)
	ready := filepath.Join(t.TempDir(), "ready")
	sock := filepath.Join(t.TempDir(), "control.sock")
	var events syncBuffer
	stdoutMu.Lock()
	old := stdout
	stdout = &events
	stdoutMu.Unlock()
	defer func() {
		stdoutMu.Lock()
		stdout = old
		stdoutMu.Unlock()
	}()
	done := make(chan int, 1)
	go func() {
		done <- run([]string{"server", "--version", "2018", "--tls-dir", filepath.Join(dir, "server"), "--fixture", fx,
			"--port", fmt.Sprint(port), "--ready-file", ready, "--control-socket", sock})
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("the server exited %d: %s", code, events.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the server is not ready")
		}
		time.Sleep(20 * time.Millisecond)
	}
	url := fmt.Sprintf("https://localhost:%d/dcap", port)
	client := func(op, tlsDir string, extra ...string) (map[string]any, int) {
		t.Helper()
		mark := len(events.String())
		code := run(append([]string{"client", op, "--version", "2018", "--url", url, "--tls-dir", filepath.Join(dir, tlsDir),
			"--fixture", fx, "--timeout-ms", "15000"}, extra...))
		lines := strings.Split(strings.TrimSpace(events.String()[mark:]), "\n")
		var doc map[string]any
		for _, l := range lines {
			var d map[string]any
			if json.Unmarshal([]byte(l), &d) == nil && d["operation"] == op {
				doc = d
			}
		}
		return doc, code
	}
	for _, op := range []string{"discover", "register", "poll-controls", "report", "respond", "log-event", "put-der", "flow-reservation"} {
		if doc, code := client(op, "client"); code != 0 || doc["ok"] != true {
			t.Errorf("%s: exit %d, %v", op, code, doc)
		}
	}
	// A fixture without tariffs: nothing to find, as the contract reads.
	if doc, code := client("poll-pricing", "client"); code != 0 || doc["ok"] != true {
		t.Errorf("poll-pricing of a fixture without tariffs: exit %d, %v", code, doc)
	}
	if doc, code := client("discover", "other-ca"); code != exitConnect || doc["error"].(map[string]any)["code"] != "connect-failed" {
		t.Errorf("other CA: exit %d, %v", code, doc)
	}

	// subscribe, triggered by call issue-control once the server reports
	// the subscription.
	notifyPort := freePort(t)
	sub := make(chan int, 1)
	go func() {
		_, code := client("subscribe", "client", "--notify-listen", fmt.Sprintf("127.0.0.1:%d", notifyPort),
			"--notify-host", fmt.Sprintf("localhost:%d", notifyPort))
		sub <- code
	}()
	for !strings.Contains(events.String(), `"subscription-created"`) {
		if time.Now().After(deadline.Add(20 * time.Second)) {
			t.Fatal("no subscription-created event")
		}
		time.Sleep(20 * time.Millisecond)
	}
	code := run([]string{"call", "issue-control", "--control-socket", sock,
		"--payload", `{"program": "Test program", "description": "Notify", "base": {"opModEnergize": true}, "duration": 600}`})
	if code != 0 {
		t.Errorf("issue-control: exit %d", code)
	}
	if code := <-sub; code != 0 {
		t.Errorf("subscribe: exit %d: %s", code, events.String())
	}
	for payload, want := range map[string]int{
		`{"program": "none", "base": {"opModEnergize": true}, "duration": 600}`: exitFailed,
		`{"base": {"opModVoltVar": true}, "duration": 600}`:                     exitUnsupported,
		`{"base": {"opModEnergize": true}}`:                                     exitFailed,
	} {
		if code := run([]string{"call", "issue-control", "--control-socket", sock, "--payload", payload}); code != want {
			t.Errorf("issue-control %s: exit %d, want %d", payload, code, want)
		}
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("SIGTERM: exit %d", code)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the server did not stop")
	}
	out := events.String()
	for _, ev := range []string{`"device-provisioned"`, `"ready"`, `"subscription-created"`, `"stopped"`} {
		if !strings.Contains(out, ev) {
			t.Errorf("no %s event", ev)
		}
	}
	if _, err := os.Stat(ready); err == nil {
		t.Error("the ready file outlived the server")
	}
}
