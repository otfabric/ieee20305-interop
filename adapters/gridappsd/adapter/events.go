// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// sep2server writes no log line when a device registers itself or
// subscribes. Its traffic capture (SEP2_TRAFFIC_CAPTURE) publishes a summary
// of every exchange on the admin route GET /api/traffic/stream
// (pkg/sep2capture/handler_sse.go), with the raw request at
// GET /api/traffic/exchanges/{id}/request: the adapter derives the
// device-registered and subscription-created events from them.

// exchangeSummary is the part of a traffic summary the adapter reads.
type exchangeSummary struct {
	ID        uint64 `json:"id"`
	ClientKey string `json:"clientKey"` // the LFDI of the connection
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
}

var subscriptionListPath = regexp.MustCompile(`^/edev/[^/]+/sub$`)

// watchTraffic opens the traffic stream and keeps reading it in the
// background, writing an event for each registration and subscription the
// server created. It returns once the stream is open, so that nothing a
// client does after the server is ready is missed.
func watchTraffic(ctx context.Context, a *adminClient) error {
	resp, err := openStream(ctx, a, "")
	if err != nil {
		return err
	}
	go func() {
		last := ""
		for {
			last = readStream(a, resp.Body, last)
			resp.Body.Close()
			for {
				time.Sleep(200 * time.Millisecond)
				resp, err = openStream(context.Background(), a, last)
				if err == nil {
					break
				}
				fmt.Fprintln(os.Stderr, "ieee20305-interop: traffic stream:", err)
			}
		}
	}()
	return nil
}

func openStream(ctx context.Context, a *adminClient, lastID string) (*http.Response, error) {
	for {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, a.base+"/api/traffic/stream", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+a.key)
		req.Header.Set("Accept", "text/event-stream")
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		if err == nil {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			err = fmt.Errorf("GET /api/traffic/stream: %s: %s", resp.Status, strings.TrimSpace(string(data)))
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// readStream reads server-sent events until the stream ends and returns the
// id of the last one.
func readStream(a *adminClient, r io.Reader, last string) string {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if id, ok := strings.CutPrefix(line, "id: "); ok {
			last = id
			continue
		}
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var sum exchangeSummary
		if json.Unmarshal([]byte(data), &sum) != nil {
			continue
		}
		go exchangeEvent(a, sum)
	}
	return last
}

func exchangeEvent(a *adminClient, sum exchangeSummary) {
	if sum.Method != http.MethodPost || sum.Status != http.StatusCreated {
		return
	}
	path, _, _ := strings.Cut(sum.Path, "?")
	switch {
	case path == "/edev":
		emit(map[string]any{"event": "device-registered", "sfdi": sfdiOfLFDI(sum.ClientKey), "lfdi": strings.ToUpper(sum.ClientKey)})
	case subscriptionListPath.MatchString(path):
		resource, err := subscribedResource(a, sum.ID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ieee20305-interop: reading a captured subscription:", err)
		}
		emit(map[string]any{"event": "subscription-created", "sfdi": sfdiOfLFDI(sum.ClientKey), "resource": resource})
	}
}

// subscribedResource reads the subscribedResource of a captured POST of a
// Subscription.
func subscribedResource(a *adminClient, id uint64) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/api/traffic/exchanges/%d/request", a.base, id), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("exchange %d: %s", id, resp.Status)
	}
	// The capture holds the request as it arrived: headers, then the body.
	body := raw
	if req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw))); err == nil {
		if b, err := io.ReadAll(req.Body); err == nil {
			body = b
		}
	}
	var sub struct {
		SubscribedResource string `xml:"subscribedResource"`
	}
	if err := xml.Unmarshal(body, &sub); err != nil {
		return "", fmt.Errorf("exchange %d: %w", id, err)
	}
	return sub.SubscribedResource, nil
}

// sfdiOfLFDI is the SFDI of an LFDI: its leftmost 36 bits as a decimal,
// zero-padded to 11 digits and followed by the check digit that makes the sum of all digits a multiple of
// 10 (IEEE 2030.5 section 6.3.3).
func sfdiOfLFDI(lfdi string) string {
	if len(lfdi) < 9 {
		return ""
	}
	v, err := strconv.ParseUint(lfdi[:9], 16, 64)
	if err != nil {
		return ""
	}
	// Zero-padded to 11 digits, as sep2tls.SFDI writes it.
	digits := fmt.Sprintf("%011d", v)
	sum := 0
	for _, d := range digits {
		sum += int(d - '0')
	}
	return digits + fmt.Sprint((10-sum%10)%10)
}
