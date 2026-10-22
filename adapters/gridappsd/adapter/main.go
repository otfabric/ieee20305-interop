// SPDX-License-Identifier: MIT

// Command ieee20305-interop is the ieee20305-interop adapter for the
// GridAPPS-D IEEE 2030.5 implementations: it runs the upstream sep2server as
// the server role and drives the upstream client library as the client role,
// behind the commands of docs/CONTAINER_CONTRACT.md.
//
// The Dockerfile builds it inside the upstream client module
// (github.com/GRIDAPPSD/ieee-2030_5-client-go), whose internal packages it
// uses to drive the upstream client.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
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
	adapterName     = "gridappsd"
	contractVersion = "0.2"
	defaultFixture  = "/fixtures/baseline/fixture.json"
	defaultReady    = "/run/ieee20305-interop/ready"
)

// Set at build time with -ldflags -X.
var (
	adapterVersion   = "dev"
	serverRevision   = "unknown"
	serverVersion    = "unknown"
	clientRevision   = "unknown"
	clientVersion    = "unknown"
	sep2serverBinary = "/usr/local/bin/sep2server"
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
		err = codedError{exitUnsupported, fmt.Errorf("call: the control socket is not implemented by the %s adapter", adapterName)}
	case "print-capabilities":
		err = printCapabilities(args[1:])
	case "print-fixture":
		err = printFixture(args[1:])
	case "version":
		if len(args) > 1 {
			err = usagef("version takes no arguments")
		} else {
			fmt.Printf("ieee20305-interop %s %s (sep2server %s, client %s)\n", adapterName, adapterVersion, serverVersion, clientVersion)
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
	allowed map[string]bool
	values  map[string]string
}

func parseFlags(args []string, allowed ...string) (*flagSet, error) {
	fs := &flagSet{allowed: map[string]bool{}, values: map[string]string{}}
	for _, a := range allowed {
		fs.allowed[a] = true
	}
	for i := 0; i < len(args); i++ {
		name, ok := strings.CutPrefix(args[i], "--")
		if !ok || name == "" {
			return nil, usagef("unexpected argument %q", args[i])
		}
		if !fs.allowed[name] {
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

// emit writes one JSON line to stdout.
func emit(v map[string]any) {
	if _, ok := v["time"]; !ok {
		v["time"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ieee20305-interop: encoding an event:", err)
		return
	}
	os.Stdout.Write(append(b, '\n'))
}
