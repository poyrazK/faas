//go:build metal

// logs_e2e_test.go — issue #254 / Move 4 end-to-end metal tripwire.
//
// What this test pins end-to-end through the real apid process:
//
//  1. The /v1/apps/{slug}/logs route is alive and gated by the
//     dashboard auth chain (Bearer token, 401 without).
//  2. The SSE response carries text/event-stream.
//  3. The apid stub emits the Move 4 "degraded" frame shape
//     when the production schedd StreamAppLogs RPC is not wired
//     in the test harness (the production-side wiring is a
//     follow-up PR; on the EX44 the schedd dials vmmd's Logs
//     RPC and the frame shape becomes `event: log`).
//
// Build tag: metal. Requires /dev/kvm + root (the apid boot
// exercises the host network listener). FAAS_TEST_KERNEL unset
// is OK — the test stubs schedd's StreamAppLogs via the apid
// surface, so no real VM boots are required.

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// logsDaemonSet is the bare-minimum daemon set required to exercise
// the /v1/apps/{slug}/logs route through the real apid process.
// The test does NOT need schedd/vmmd — the apid stub
// (cmd/apid/schedd_client.go::stubScheddClient) returns
// codes.Unimplemented from StreamAppLogs, so the handler emits
// the degraded frame without dialling a real schedd. Boot just
// apid; that also avoids the 30s schedd boot (cmd-e2e-schedd-
// migration-race memory).
const logsDaemonSet = e2etest.APID

// TestAppLogsSSE_DegradedFrame pins the Move 4 apid stub wire
// shape end-to-end through the real apid process. The stub
// returns codes.Unimplemented from StreamAppLogs; the apid
// handler translates that to a `event: degraded` SSE frame so
// the dashboard's htmx-ext-sse consumer can render a friendly
// "schedd StreamAppLogs wiring pending" notice instead of
// streaming nothing.
//
// What we don't pin here: the producer-side path (ring →
// vmmd Logs RPC → real schedd fan-out). That requires the
// production schedd wiring and a guest that emits to /dev/
// console — both ship in the follow-up PR. The current test
// verifies the SSE handshake, the auth chain, and the JSON
// envelope so a future regression in the apid-side path trips
// the test even before the producer side lands.
func TestAppLogsSSE_DegradedFrame(t *testing.T) {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, logsDaemonSet)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	const slug = "loghello"
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app"}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}

	// Open the SSE stream. We can't use doReq (it Reads the
	// full body) — the SSE stream is long-lived. Use a raw
	// HTTP round trip so we can read the first frame and
	// close the connection.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.APIDURL+"/v1/apps/"+slug+"/logs?follow=1", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/apps/%s/logs: %v", slug, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if err := expectDegradedFrame(t, resp.Body, 3*time.Second); err != nil {
		t.Fatalf("degraded frame: %v", err)
	}
}

// TestAppLogsSSE_AuthWiringRejectsAnonymous verifies the
// /v1/apps/{slug}/logs route is gated by the dashboard auth
// chain — a request without a Bearer token must 401. PR #180
// (memory gatewayd-isapidpath-pr180-gap) proved that any new
// apid public route needs a parallel isApidPath entry, but the
// *SDK auth* chain is layered on top of that — a missed
// middleware would surface as a 200 with an empty body, not a
// 401. This test catches the "anonymous tail" regression.
func TestAppLogsSSE_AuthWiringRejectsAnonymous(t *testing.T) {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, logsDaemonSet)

	// No Authorization header — dial anonymously.
	req, err := http.NewRequest(http.MethodGet, h.APIDURL+"/v1/apps/never/logs?follow=0", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("anon GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		t.Errorf("anon /v1/apps/never/logs returned %d, want 401 (body=%s)", resp.StatusCode, strings.TrimSpace(string(data)))
	}
}

// TestAppLogsSSE_MissingAppProblem verifies the apid handler
// renders a 404 Problem (RFC 7807) when the slug doesn't belong
// to the caller's account. The wire shape is the canonical
// "code=not_found, status=404" envelope the SDK reads as
// *APIError. A regression here trips the SDK's
// `errors.As(err, &api.APIError)` branch and the CLI prints a
// stack trace instead of a friendly "no such app".
func TestAppLogsSSE_MissingAppProblem(t *testing.T) {
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.Start(t, pool, logsDaemonSet)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	_, status := doReq(t, h, key, http.MethodGet, "/v1/apps/missing/logs?follow=0", nil)
	if status != http.StatusNotFound {
		t.Errorf("missing app: status=%d, want 404", status)
	}
}

// expectDegradedFrame blocks up to timeout reading from the SSE
// body for one `event: degraded` frame. Pins the Move 4 stub
// wire shape end-to-end so a regression in the apid handler
// (e.g. switching to codes.PermissionDenied or omitting the
// event: degraded frame) trips before the dashboard panel goes
// dark. Decodes the frame's data field as JSON and asserts the
// `error` subfield is present — but doesn't pin the exact error
// string (the stub's exact wording is incidental).
func expectDegradedFrame(t *testing.T, body io.Reader, timeout time.Duration) error {
	t.Helper()
	type frame struct {
		event string
		data  string
	}
	done := make(chan frame, 1)
	errs := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(body)
		// Allow long lines (max SSE field size is unbounded
		// per WHATWG; practical cap is 64 KB which fits apid's
		// largest frame).
		scanner.Buffer(make([]byte, 0, 4096), 64*1024)
		var (
			event string
			data  []string
		)
		flush := func() {
			if event == "" && len(data) == 0 {
				return
			}
			done <- frame{event: event, data: strings.Join(data, "\n")}
			event = ""
			data = nil
		}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				flush()
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			colon := strings.IndexByte(line, ':')
			if colon < 0 {
				continue
			}
			field := line[:colon]
			value := line[colon+1:]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			switch field {
			case "event":
				event = value
			case "data":
				data = append(data, value)
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- err
			return
		}
		flush()
	}()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case f := <-done:
		if f.event != "degraded" {
			return fmt.Errorf("got event=%q data=%q, want event=degraded", f.event, f.data)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(f.data), &payload); err != nil {
			return fmt.Errorf("degraded payload not JSON: %w (data=%q)", err, f.data)
		}
		if _, ok := payload["error"]; !ok {
			return fmt.Errorf("degraded payload missing error field: %q", f.data)
		}
		return nil
	case err := <-errs:
		return fmt.Errorf("SSE scanner error: %w", err)
	case <-deadline.C:
		return fmt.Errorf("no `event: degraded` frame arrived within %v", timeout)
	}
}
