// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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
var adminFunctions = map[string]bool{"opModConnect": true, "opModEnergize": true, "opModMaxLimW": true}

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
	err = waitListening(ctx, fmt.Sprintf("127.0.0.1:%d", adminPort))
	if err == nil {
		err = provision(ctx, admin, fixture, devices)
	}
	cancel()
	if err != nil {
		return stop(exitFailed, fmt.Errorf("provisioning sep2server: %w", err))
	}
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

func checkServerFixture(f *Fixture) error {
	if len(f.DERPrograms) != 1 {
		return codedError{exitUnsupported, fmt.Errorf("the %s server provisions one DER program per device; the fixture has %d", adapterName, len(f.DERPrograms))}
	}
	p := f.DERPrograms[0]
	check := func(c *Control) error {
		for name := range c.Base {
			if !adminFunctions[name] {
				return codedError{exitUnsupported, fmt.Errorf("the %s server cannot issue the DERControlBase element %s", adapterName, name)}
			}
		}
		return nil
	}
	if p.DefaultControl != nil {
		if err := check(p.DefaultControl); err != nil {
			return err
		}
	}
	for i := range p.Controls {
		if err := check(&p.Controls[i]); err != nil {
			return err
		}
	}
	return nil
}

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

// provision creates each device with the fixture's PIN, then its DER
// program, default control and controls.
func provision(ctx context.Context, a *adminClient, f *Fixture, devices []provisionedDevice) error {
	program := f.DERPrograms[0]
	now := time.Now().Unix()
	for _, d := range devices {
		var created struct {
			Href string `json:"href"`
		}
		err := a.call(ctx, http.MethodPost, "/api/devices", map[string]any{
			"sfdi": d.SFDI, "lfdi": d.LFDI, "pin": f.Device.PIN, "enabled": true,
		}, &created)
		if err != nil {
			return err
		}
		id := strings.TrimPrefix(created.Href, "/edev/")
		var programHref string
		issue := func(body map[string]any) error {
			var reply struct {
				Program json.RawMessage `json:"program"`
			}
			if err := a.call(ctx, http.MethodPost, "/api/devices/"+id+"/controls", body, &reply); err != nil {
				return err
			}
			if programHref == "" {
				programHref = hrefOf(reply.Program)
			}
			return nil
		}
		if c := program.DefaultControl; c != nil {
			if err := issue(map[string]any{"kind": "default", "functions": c.Base}); err != nil {
				return err
			}
		}
		for _, c := range program.Controls {
			if err := issue(map[string]any{
				"kind": "scheduled", "functions": c.Base, "durationSeconds": c.Duration, "description": c.Description,
				"startTime": now + int64(c.Start),
			}); err != nil {
				return err
			}
		}
		if programHref == "" {
			continue
		}
		derpID := programHref[strings.LastIndexByte(programHref, '/')+1:]
		err = a.call(ctx, http.MethodPut, "/api/devices/"+id+"/der-programs/"+derpID, map[string]any{
			"description": program.Description, "primacy": program.Primacy,
		}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

// hrefOf returns the href of a program in an admin reply, which is either
// an object with an href or the href itself.
func hrefOf(raw json.RawMessage) string {
	var obj struct {
		Href string `json:"href"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Href != "" {
		return obj.Href
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
