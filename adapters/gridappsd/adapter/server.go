// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	sepTLS "github.com/GRIDAPPSD/ieee-2030_5-core-go/pkg/sep2tls"
)

// adminFunctions are the DERControlBase elements the sep2server admin API
// takes in a control's functions.
var adminFunctions = map[string]bool{"opModConnect": true, "opModEnergize": true, "opModMaxLimW": true, "opModFixedW": true}

// provisionedDevice is a device the server provisions from --tls-dir/devices.
type provisionedDevice struct {
	File string
	LFDI string
	SFDI string
}

func runServer(args []string) error {
	fs, err := parseFlags(args, "version", "port", "tls-dir", "fixture", "ready-file", "control-socket")
	if err != nil {
		return err
	}
	edition, err := fs.edition()
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
	fixturePath := fs.get("fixture", defaultFixture)
	fixture, _, err := loadFixture(fixturePath)
	if err != nil {
		return err
	}
	if err := checkServerFixture(fixture); err != nil {
		return err
	}
	for _, f := range []string{"server.crt", "server.key", "ca.crt"} {
		if _, err := os.Stat(filepath.Join(tlsDir, f)); err != nil {
			return codedError{exitFixture, fmt.Errorf("--tls-dir: %w", err)}
		}
	}
	devices, err := loadDevices(filepath.Join(tlsDir, "devices"))
	if err != nil {
		return err
	}
	readyFile := fs.get("ready-file", defaultReady)
	controlSocket := fs.get("control-socket", defaultControlSocket)

	adminPort, err := freePort()
	if err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	admin := &adminClient{base: fmt.Sprintf("http://127.0.0.1:%d", adminPort), key: hex.EncodeToString(key)}

	// sep2server starts its admin listener only when it can mint
	// certificates, which needs a CA key. The test CA's key never leaves the
	// test, so the serving-CA role, which only mints, gets a throwaway CA of
	// its own; the test CA stays the CA that verifies devices.
	mintDir, err := os.MkdirTemp("", "sep2-serving-ca-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mintDir)
	if err := writeThrowawayCA(mintDir); err != nil {
		return fmt.Errorf("throwaway serving CA: %w", err)
	}
	// The traffic capture is how the adapter sees registrations and
	// subscriptions: sep2server logs neither.
	trafficDir, err := os.MkdirTemp("", "sep2-traffic-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(trafficDir)

	cmd := exec.Command(sep2serverBinary, "serve")
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("SEP2_ADDR=:%d", port),
		"SEP2_CERT_DIR="+tlsDir,
		"SEP2_SERVING_CA="+filepath.Join(mintDir, "ca.crt"),
		"SEP2_SERVING_CA_KEY="+filepath.Join(mintDir, "ca.key"),
		"SEP2_EDITION="+edition,
		"SEP2_DATA_DIR=",
		fmt.Sprintf("SEP2_ADMIN_LISTEN=127.0.0.1:%d", adminPort),
		"SEP2_ADMIN_KEY="+admin.key,
		"SEP2_NOTIFICATION_ALLOW_LOOPBACK=true",
		"SEP2_TRAFFIC_CAPTURE=true",
		"SEP2_TRAFFIC_DIR="+trafficDir,
		// sep2server delivers notifications with Go's standard client,
		// which verifies the listener against the system roots: make the
		// test CA the root it trusts.
		"SSL_CERT_FILE="+filepath.Join(tlsDir, "ca.crt"),
		// The IEEE 2030.5 test PEN: sep2server issues no control without a PEN.
		"SEP2_PEN=40732",
		"HOME=/tmp",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting sep2server: %w", err)
	}
	listening := make(chan struct{})
	go relayLog(stdout, listening)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)

	stop := func(code int, cause error) error {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		_ = os.Remove(readyFile)
		emit(map[string]any{"event": "stopped"})
		if cause == nil {
			return nil
		}
		return codedError{code, cause}
	}

	select {
	case <-listening:
	case err := <-exited:
		return fmt.Errorf("sep2server exited before it listened: %v", err)
	case <-signals:
		return stop(exitOK, nil)
	case <-time.After(60 * time.Second):
		return stop(exitFailed, errors.New("sep2server did not listen within 60 s"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	var seeded []seededDevice
	err = waitListening(ctx, fmt.Sprintf("127.0.0.1:%d", adminPort))
	if err == nil {
		err = watchTraffic(ctx, admin)
	}
	if err == nil {
		seeded, err = provision(ctx, admin, fixture, devices)
	}
	cancel()
	if err != nil {
		return stop(exitFailed, fmt.Errorf("provisioning sep2server: %w", err))
	}
	ctl, err := listenControl(controlSocket, &controller{admin: admin, fixture: fixture, devices: seeded})
	if err != nil {
		return stop(exitFailed, fmt.Errorf("control socket: %w", err))
	}
	defer ctl.Close()
	for _, d := range devices {
		emit(map[string]any{"event": "device-provisioned", "sfdi": d.SFDI, "lfdi": d.LFDI})
	}
	if err := os.WriteFile(readyFile, nil, 0o644); err != nil {
		return stop(exitFailed, fmt.Errorf("ready file: %w", err))
	}
	emit(map[string]any{"event": "ready", "adapter": adapterName, "version": edition, "port": port, "fixture": fixture.Name})

	select {
	case <-signals:
		return stop(exitOK, nil)
	case err := <-exited:
		_ = os.Remove(readyFile)
		emit(map[string]any{"event": "stopped"})
		return fmt.Errorf("sep2server exited: %v", err)
	}
}

// checkServerFixture refuses (exit 5) what the sep2server admin API cannot
// seed: tariffs, messages, CSIP-AUS limits, curves, control functions
// outside its catalog, and programs or controls outside its limits.
func checkServerFixture(f *Fixture) error {
	unsupported := func(format string, a ...any) error {
		return codedError{exitUnsupported, fmt.Errorf("the %s server cannot seed the fixture %s: %s", adapterName, f.Name, fmt.Sprintf(format, a...))}
	}
	if len(f.Tariffs) > 0 {
		return unsupported("tariffs: sep2server has no pricing function set and no admin route to seed one")
	}
	if len(f.Messages) > 0 {
		return unsupported("messages: the sep2server admin API has no route to seed MessagingPrograms")
	}
	for _, p := range f.DERPrograms {
		if !validAdminText(p.Description) {
			return unsupported("program description %q is not 1 to 32 printable ASCII characters", p.Description)
		}
		if !validPrimacy(int(p.Primacy)) {
			return unsupported("program %q has primacy %d; the admin API takes 0, 1, 2 or 65 to 191", p.Description, p.Primacy)
		}
		if len(p.Curves) > 0 {
			return unsupported("program %q has curves: %s", p.Description, curvesUnsupported)
		}
		if p.DefaultControl != nil {
			if err := checkBase(p.DefaultControl.Base); err != nil {
				return unsupported("default control of %q: %v", p.Description, err)
			}
		}
		for _, c := range p.Controls {
			if err := checkBase(c.Base); err != nil {
				return unsupported("control %q of %q: %v", c.Description, p.Description, err)
			}
			if err := checkSchedule(c.Duration, c.Start); err != nil {
				return unsupported("control %q of %q: %v", c.Description, p.Description, err)
			}
		}
	}
	return nil
}

// curvesUnsupported says why the server refuses curves.
const curvesUnsupported = "sep2server stores curves only from its boot fixture and serves them only as the list GET /dc " +
	"(pkg/sep2srv/assembly/assembly.go:1049); a control's curve link /dc/{id} is not served, and an admin-created program has no DERCurveListLink"

// checkBase refuses DERControlBase elements the admin API does not issue.
func checkBase(base map[string]json.RawMessage) error {
	for name := range base {
		if !adminFunctions[name] {
			return fmt.Errorf("the admin API does not issue the DERControlBase element %s", name)
		}
	}
	return nil
}

// Limits of POST /api/der/controls (internal/handler/admin_dercontrol.go).
const (
	minControlDuration = 60
	maxControlDuration = 24 * 3600
	maxControlAhead    = 7 * 24 * 3600
)

func checkSchedule(duration, start uint32) error {
	if duration < minControlDuration || duration > maxControlDuration {
		return fmt.Errorf("duration %d s is outside the admin API's 60 s to 24 h", duration)
	}
	if start > maxControlAhead {
		return fmt.Errorf("start %d s is more than the admin API's 7 days ahead", start)
	}
	return nil
}

// validAdminText is the admin API's description rule: 1 to 32 printable
// ASCII characters.
func validAdminText(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// validPrimacy is the admin API's primacy rule: the defined values 0 to 2
// or the user range 65 to 191.
func validPrimacy(p int) bool { return (p >= 0 && p <= 2) || (p >= 65 && p <= 191) }

// loadDevices reads the device certificates the server provisions.
func loadDevices(dir string) ([]provisionedDevice, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, codedError{exitFixture, fmt.Errorf("--tls-dir: %w", err)}
	}
	var out []provisionedDevice
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".crt") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, codedError{exitFixture, err}
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, codedError{exitFixture, fmt.Errorf("%s: no PEM certificate", e.Name())}
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, codedError{exitFixture, fmt.Errorf("%s: %w", e.Name(), err)}
		}
		out = append(out, provisionedDevice{File: e.Name(), LFDI: sepTLS.LFDI(cert), SFDI: sepTLS.SFDI(cert)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

// relayLog copies sep2server's log to stderr, and closes listening once
// the server reports that it listens.
func relayLog(r io.Reader, listening chan<- struct{}) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	closed := false
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintln(os.Stderr, "sep2server:", line)
		var entry struct {
			Msg string `json:"msg"`
		}
		if json.Unmarshal([]byte(line), &entry) != nil {
			continue
		}
		if !closed && strings.Contains(entry.Msg, "connection details") {
			close(listening)
			closed = true
		}
		if strings.Contains(entry.Msg, "TLS handshake error") {
			emit(map[string]any{"event": "connection-rejected", "reason": entry.Msg})
		}
	}
}

// writeThrowawayCA writes a self-signed P-256 CA, ca.crt and ca.key, to dir.
func writeThrowawayCA(dir string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "ieee20305-interop throwaway serving CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "ca.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600)
}

// waitListening waits until addr accepts TCP connections: sep2server
// announces its device listener before its admin listener accepts.
func waitListening(ctx context.Context, addr string) error {
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the admin listener %s did not accept connections: %w", addr, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// adminClient calls the sep2server admin API.
type adminClient struct {
	base string
	key  string
}

func (a *adminClient) call(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("%s %s: decoding %s: %w", method, path, data, err)
		}
	}
	return nil
}

// seededDevice is what provision created for one device.
type seededDevice struct {
	provisionedDevice
	ID       string            // the EndDevice id, /edev/{ID}
	Programs map[string]string // program href by description
}

// provision creates each device with the fixture's PIN, assigns it one FSA,
// and creates the fixture's programs under it with their default controls
// and controls.
func provision(ctx context.Context, a *adminClient, f *Fixture, devices []provisionedDevice) ([]seededDevice, error) {
	now := time.Now().Unix()
	var fsa struct {
		Href string `json:"href"`
	}
	// The admin API takes the mRID as the FSA's id, and serves it as the
	// mRID: give it a valid mRIDType (32 hex digits), not its own default
	// "fsa-<hex>" id.
	sum := sha256.Sum256([]byte("ieee20305-interop FSA " + f.Name))
	fsaMRID := strings.ToUpper(hex.EncodeToString(sum[:16]))
	if err := a.call(ctx, http.MethodPost, "/api/fsas", map[string]any{"description": "ieee20305-interop", "primacy": 0, "mRID": fsaMRID}, &fsa); err != nil {
		return nil, err
	}
	fsaID := fsa.Href[strings.LastIndexByte(fsa.Href, '/')+1:]
	var out []seededDevice
	for _, d := range devices {
		var created struct {
			Href string `json:"href"`
		}
		err := a.call(ctx, http.MethodPost, "/api/devices", map[string]any{
			"sfdi": d.SFDI, "lfdi": d.LFDI, "pin": f.Device.PIN, "enabled": true,
		}, &created)
		if err != nil {
			return nil, err
		}
		sd := seededDevice{provisionedDevice: d, ID: strings.TrimPrefix(created.Href, "/edev/"), Programs: map[string]string{}}
		if err := a.call(ctx, http.MethodPost, "/api/devices/"+sd.ID+"/fsa-assignment", map[string]any{"fsaHref": fsa.Href}, nil); err != nil {
			return nil, err
		}
		for _, p := range f.DERPrograms {
			// The admin API creates a program only with a default control;
			// one the fixture does not have is cleared right after.
			def := map[string]json.RawMessage{"opModConnect": json.RawMessage("true")}
			if p.DefaultControl != nil {
				def = p.DefaultControl.Base
			}
			var prog struct {
				ID   string `json:"id"`
				Href string `json:"href"`
			}
			err := a.call(ctx, http.MethodPost, "/api/devices/"+sd.ID+"/fsas/"+fsaID+"/der-programs", map[string]any{
				"description": p.Description, "primacy": p.Primacy, "mRID": p.MRID,
				"defaultControl": map[string]any{"functions": def},
			}, &prog)
			if err != nil {
				return nil, err
			}
			if p.DefaultControl == nil {
				err := a.call(ctx, http.MethodPut, "/api/devices/"+sd.ID+"/der-programs/"+prog.ID, map[string]any{
					"description": p.Description, "primacy": p.Primacy, "defaultControl": nil,
				}, nil)
				if err != nil {
					return nil, err
				}
			}
			sd.Programs[p.Description] = prog.Href
			for _, c := range p.Controls {
				var start *int64
				if c.Start > 0 {
					t := now + int64(c.Start)
					start = &t
				}
				if _, err := issueControl(ctx, a, prog.Href, c.Base, c.Duration, start, c.Description); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, sd)
	}
	return out, nil
}

// issueControl issues a DERControl to a program through the admin API.
// A nil start starts it now.
func issueControl(ctx context.Context, a *adminClient, programHref string, base map[string]json.RawMessage, duration uint32, start *int64, description string) (map[string]any, error) {
	body := map[string]any{
		"derProgramHref": programHref, "functions": base, "durationSeconds": duration,
	}
	if description != "" {
		body["description"] = description
	}
	if start != nil {
		body["startTime"] = *start
	}
	var created map[string]any
	if err := a.call(ctx, http.MethodPost, "/api/der/controls", body, &created); err != nil {
		return nil, err
	}
	return created, nil
}
