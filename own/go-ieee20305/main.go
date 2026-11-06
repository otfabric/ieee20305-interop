// SPDX-License-Identifier: MIT

// Command ieee20305-interop is the ieee20305-interop-otfabric image:
// go-ieee20305, OT Fabric's own IEEE 2030.5 stack, behind the container
// contract (docs/CONTAINER_CONTRACT.md). The server role is the library's
// server package, the client role its client package; both speak
// TLS_ECDHE_ECDSA_WITH_AES_128_CCM_8 through its tlsccm module.
//
// It is OT Fabric's own stack, not an independent implementation: results
// against it are never independent evidence for go-ieee20305 (README.md).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Exit codes of the contract.
const (
	exitOK          = 0
	exitFailed      = 1
	exitUsage       = 2
	exitConnect     = 3
	exitFixture     = 4
	exitUnsupported = 5
)

const (
	adapterName     = "otfabric"
	contractVersion = "0.3"
	fixtureRoot     = "/fixtures"
	defaultReady    = "/run/ieee20305-interop/ready"
)

var defaultFixture = fixtureRoot + "/baseline/fixture.json"

// Set at build time with -ldflags -X.
var (
	adapterVersion   = "dev"
	upstreamVersion  = "dev"
	upstreamRevision = "dev"
)

// usageError is a malformed invocation: exit 2.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

// codedError ends the command with a contract exit code.
type codedError struct {
	code int
	err  error
}

func (e codedError) Error() string { return e.err.Error() }

func (e codedError) Unwrap() error { return e.err }

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: ieee20305-interop server|client|call|print-capabilities|print-fixture|version")
		return exitUsage
	}
	var err error
	switch args[0] {
	case "server":
		err = runServer(args[1:])
	case "client":
		return runClient(args[1:])
	case "call":
		return runCall(args[1:])
	case "print-capabilities":
		err = printCapabilities(args[1:])
	case "print-fixture":
		err = printFixture(args[1:])
	case "version":
		if len(args) > 1 {
			err = usagef("version takes no arguments")
		} else {
			fmt.Fprintf(stdout, "ieee20305-interop %s %s (go-ieee20305 %s, %s)\n", adapterName, adapterVersion, upstreamVersion, upstreamRevision)
		}
	default:
		err = usagef("unknown command %q", args[0])
	}
	return exitCode(err)
}

func exitCode(err error) int {
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(os.Stderr, "ieee20305-interop:", err)
	switch e := err.(type) {
	case usageError:
		return exitUsage
	case codedError:
		return e.code
	}
	return exitFailed
}

// flagSet parses the flags of the contract: --name value, each at most once,
// none unknown. Every flag takes a value.
type flagSet struct {
	values map[string]string
}

func parseFlags(args []string, allowed ...string) (*flagSet, error) {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	fs := &flagSet{values: map[string]string{}}
	for i := 0; i < len(args); i++ {
		name, isFlag := strings.CutPrefix(args[i], "--")
		if !isFlag || name == "" {
			return nil, usagef("unexpected argument %q", args[i])
		}
		if !ok[name] {
			return nil, usagef("unknown flag --%s", name)
		}
		if _, dup := fs.values[name]; dup {
			return nil, usagef("flag --%s given twice", name)
		}
		if i+1 >= len(args) {
			return nil, usagef("flag --%s needs a value", name)
		}
		fs.values[name] = args[i+1]
		i++
	}
	return fs, nil
}

func (fs *flagSet) get(name, def string) string {
	if v, ok := fs.values[name]; ok {
		return v
	}
	return def
}

func (fs *flagSet) required(name string) (string, error) {
	v, ok := fs.values[name]
	if !ok || v == "" {
		return "", usagef("--%s is required", name)
	}
	return v, nil
}

// edition reads --version: the IEEE 2030.5 edition.
func (fs *flagSet) edition() (string, error) {
	v, err := fs.required("version")
	if err != nil {
		return "", err
	}
	if v != "2018" && v != "2023" {
		return "", usagef("--version %q is not 2018 or 2023", v)
	}
	return v, nil
}

func (fs *flagSet) millis(name string, def time.Duration) (time.Duration, error) {
	v, ok := fs.values[name]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, usagef("--%s %q is not a positive number of milliseconds", name, v)
	}
	return time.Duration(n) * time.Millisecond, nil
}

var (
	stdoutMu sync.Mutex
	stdout   io.Writer = os.Stdout
)

// emit writes one JSON line to stdout; an event gets its time.
func emit(v map[string]any) {
	if _, ok := v["event"]; ok {
		if _, ok := v["time"]; !ok {
			v["time"] = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "ieee20305-interop: encoding a document:", err)
		return
	}
	stdoutMu.Lock()
	defer stdoutMu.Unlock()
	_, _ = stdout.Write(buf.Bytes())
}
