// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	ieee20305 "github.com/otfabric/go-ieee20305"
	"github.com/otfabric/go-ieee20305/csipaus"
	"github.com/otfabric/go-ieee20305/server"
)

const defaultControlSocket = "/run/ieee20305-interop/control.sock"

// controlRequest is one line a `call` sends through the control socket.
type controlRequest struct {
	Operation string          `json:"operation"`
	Device    string          `json:"device,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// controlReply is the line the server answers with: the operation's part of
// the result document.
type controlReply struct {
	OK       bool           `json:"ok"`
	Error    *resultError   `json:"error"`
	Request  map[string]any `json:"request"`
	Response map[string]any `json:"response"`
}

// controller performs the `call` operations of the server.
type controller struct {
	srv     *server.Server
	fixture *fixture
	devices []seededDevice
	mu      sync.Mutex // one call at a time
}

// listenControl serves the control socket until the listener is closed.
func listenControl(path string, c *controller) (net.Listener, error) {
	_ = os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go c.serve(conn)
		}
	}()
	return l, nil
}

func (c *controller) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	var reply controlReply
	var req controlRequest
	if err != nil || json.Unmarshal(line, &req) != nil {
		reply.Error = &resultError{Code: "invalid-request", Detail: "the control socket takes one JSON request line"}
	} else {
		c.mu.Lock()
		reply = c.perform(req)
		c.mu.Unlock()
	}
	b, _ := json.Marshal(reply)
	_, _ = conn.Write(append(b, '\n'))
}

func (c *controller) perform(req controlRequest) controlReply {
	if req.Operation == "issue-control" {
		return c.issueControl(req)
	}
	return controlReply{Error: &resultError{Code: "unsupported", Detail: fmt.Sprintf("the %s server does not offer the call %q", adapterName, req.Operation)}}
}

// issueControl issues a control, the payload of the call, to the program
// it names on the device of --device, or on every provisioned device.
func (c *controller) issueControl(req controlRequest) controlReply {
	invalid := func(format string, a ...any) controlReply {
		return controlReply{Error: &resultError{Code: "invalid-request", Detail: fmt.Sprintf(format, a...)}}
	}
	var p struct {
		Program     *string        `json:"program"`
		Description string         `json:"description"`
		Base        map[string]any `json:"base"`
		Duration    uint32         `json:"duration"`
		Start       uint32         `json:"start"`
	}
	if len(req.Payload) == 0 {
		return invalid("issue-control needs a --payload: a control and the program to issue it to")
	}
	if err := json.Unmarshal(req.Payload, &p); err != nil {
		return invalid("--payload: %v", err)
	}
	program := c.fixture.DERPrograms[0].Description
	if p.Program != nil {
		program = *p.Program
	}
	if len(p.Base) == 0 {
		return invalid("--payload: base needs at least one DERControlBase element")
	}
	b, err := base(p.Base)
	if err != nil {
		var coded codedError
		if errors.As(err, &coded) && coded.code == exitUnsupported {
			return controlReply{Error: &resultError{Code: "unsupported", Detail: err.Error()}}
		}
		return invalid("--payload: %v", err)
	}
	if p.Duration == 0 {
		return invalid("--payload: duration must be at least 1 s")
	}
	if len(p.Description) > 32 {
		return invalid("--payload: description %q is longer than 32 characters", p.Description)
	}
	targets := c.devices
	if req.Device != "" {
		targets = nil
		for _, d := range c.devices {
			if d.SFDI == req.Device {
				targets = append(targets, d)
			}
		}
		if len(targets) == 0 {
			return invalid("no provisioned device has the SFDI %s", req.Device)
		}
	}
	if len(targets) == 0 {
		return invalid("the server has no provisioned device")
	}
	for _, d := range targets {
		if _, ok := d.Programs[program]; !ok {
			return invalid("the fixture %s has no DER program %q", c.fixture.Name, program)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a := c.srv.Admin()
	aus := c.fixture.csipAUS()
	reply := controlReply{}
	var issued []map[string]any
	for _, d := range targets {
		href := d.Programs[program]
		reply.Request = map[string]any{"method": "POST", "path": href + "/derc"}
		ctl := &csipaus.DERControl{DERControlBase: b}
		ctl.MRID = make(ieee20305.MRIDType, 16)
		_, _ = rand.Read(ctl.MRID)
		if p.Description != "" {
			ctl.Description = str32(p.Description)
		}
		now := a.Now().Unix()
		ctl.CreationTime = ieee20305.TimeType(now)
		ctl.Interval = ieee20305.DateTimeInterval{Start: ieee20305.TimeType(now + int64(p.Start)), Duration: ieee20305.UInt32(p.Duration)}
		replyTo, flags := d.ReplyTo, ieee20305.HexBinary8{0x03}
		ctl.ReplyTo, ctl.ResponseRequired = &replyTo, &flags
		created, err := a.Create(ctx, href+"/derc", derControl(ctl, aus), "")
		if err == nil {
			// Write the program again: the subscribers of the DERProgramList
			// are notified of changes of its members.
			var n *server.Node
			if n, err = a.Get(ctx, href); err == nil {
				err = a.Put(ctx, n)
			}
		}
		if err != nil {
			reply.Error = &resultError{Code: "server-error", Detail: err.Error()}
			return reply
		}
		issued = append(issued, map[string]any{"sfdi": d.SFDI, "program": href, "href": created,
			"mRID": strings.ToUpper(fmt.Sprintf("%x", []byte(ctl.MRID)))})
	}
	reply.OK = true
	reply.Response = map[string]any{"status": 201, "body": map[string]any{"controls": issued}}
	return reply
}

// runCall is the call command: it sends the operation through the control
// socket of the running server and prints the result document.
func runCall(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "--") {
		return exitCode(usagef("call needs an operation"))
	}
	op := args[0]
	fs, err := parseFlags(args[1:], "device", "payload", "timeout-ms", "control-socket")
	if err != nil {
		return exitCode(err)
	}
	timeout, err := fs.millis("timeout-ms", 30*time.Second)
	if err != nil {
		return exitCode(err)
	}
	req := controlRequest{Operation: op, Device: fs.get("device", "")}
	if v, ok := fs.values["payload"]; ok {
		if !json.Valid([]byte(v)) {
			return exitCode(usagef("--payload is not JSON"))
		}
		req.Payload = json.RawMessage(v)
	}
	start := time.Now()
	var reply controlReply
	code := exitOK
	if op != "issue-control" {
		reply.Error = &resultError{Code: "unsupported", Detail: fmt.Sprintf("the %s adapter offers no call %q", adapterName, op)}
	} else {
		reply, err = callSocket(fs.get("control-socket", defaultControlSocket), req, timeout)
		if err != nil {
			reply = controlReply{Error: &resultError{Code: "not-connected", Detail: err.Error()}}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				reply.Error.Code = "timeout"
			}
		}
	}
	switch {
	case reply.OK:
	case reply.Error == nil:
		reply.Error = &resultError{Code: "server-error", Detail: "the server answered without a result"}
		code = exitFailed
	case reply.Error.Code == "unsupported":
		code = exitUnsupported
	default:
		code = exitFailed
	}
	emit(map[string]any{
		"schemaVersion": "1.0",
		"adapter":       adapterName,
		"operation":     op,
		"ok":            reply.OK,
		"error":         reply.Error,
		"request":       reply.Request,
		"response":      reply.Response,
		"elapsedMs":     time.Since(start).Milliseconds(),
	})
	return code
}

func callSocket(path string, req controlRequest, timeout time.Duration) (controlReply, error) {
	var reply controlReply
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return reply, fmt.Errorf("the control socket %s does not answer: %w", path, err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	b, _ := json.Marshal(req)
	if _, err := conn.Write(append(b, '\n')); err != nil {
		return reply, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return reply, fmt.Errorf("the control socket %s did not answer: %w", path, err)
	}
	if err := json.Unmarshal(line, &reply); err != nil {
		return reply, fmt.Errorf("the control socket %s answered %q: %w", path, line, err)
	}
	return reply, nil
}
