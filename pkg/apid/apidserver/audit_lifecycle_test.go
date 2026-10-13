// Integration tests for the full async-batched failed-login audit
// lifecycle (issue #286 review fix #5): WithOpsMetrics -> Start ->
// handler emit -> flush -> Close. The audit_async_test.go unit
// tests cover the channel mechanics in isolation; this file
// exercises the wiring through the real cmd/apid/server entry
// points so a regression in either the constructor, WithOpsMetrics,
// or the flusher goroutine integration trips a test.
//
// The handler-level harness in handlers_auth_login_test.go does
// NOT call WithOpsMetrics (its harness is the production-shape
// sans metrics path, so tests can run without a Prometheus
// registry). This file opts into the metrics path explicitly.

package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
	"github.com/onebox-faas/faas/pkg/wire"
)

// failingAppendStore wraps a state.Store so every AppendEvent
// returns an error. The seam at audit.go::flushOne must then
// increment the dedicated failedLoginAuditWriteFailures counter
// rather than the success-path auditWriteFailures counter (the
// review fix #3 contract). Same shape as failingFlushStore in
// audit_async_test.go but kept here so this file is self-contained.
type failingAppendStore struct {
	Store state.Store
}

func (failingAppendStore) AppendEvent(_ context.Context, _, _ string, _ *string, _ []byte) error {
	return errFailingAppend
}

// AppendEventWithTrace (PR-#TBD / C2) — C4's audit emit path
// routes through this method.
func (failingAppendStore) AppendEventWithTrace(_ context.Context, _, _ string, _ *string, _ []byte, _ *string) error {
	return errFailingAppend
}

var errFailingAppend = failingAppendError{"simulated AppendEvent failure"}

type failingAppendError struct{ msg string }

func (e failingAppendError) Error() string { return e.msg }

// TestAuditLifecycle_StartFlushClose exercises the full
// WithOpsMetrics -> Start -> handler emit -> Close pipeline
// against a healthy store. The audit row MUST land in the events
// table through the async flusher (proving Start actually wired
// the goroutine, not just the channel), and the per-IP counter
// MUST increment (proving the failedOps surface was bound).
func TestAuditLifecycle_StartFlushClose(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid")
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
		WithOpsMetrics(context.Background(), ops)
	defer (*srv.Audit()).Close()

	h := srv.Handler()
	form := url.Values{"email": {"nobody@example.com"}, "password": {"any-password-1234567890"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("User-Agent", "lifecycle-test/1.0")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// The handler returns 401 regardless of the audit row landing.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/login status = %d, want 401 (body=%s)", rec.Code, rec.Body.String())
	}
	// Body shape is the standard invalid_credentials problem.
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got := body["code"]; got != api.CodeInvalidCredentials {
		t.Errorf("body code = %v, want %q", got, api.CodeInvalidCredentials)
	}

	// Wait for the flusher to drain. The production cadence is
	// 250 ms; allow up to 1s for goroutine scheduling.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		events, err := store.ListEvents(context.Background(), "", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			if e.Kind == apidserver.KindAuthLoginFailed {
				// The row landed through the full Start ->
				// flusher -> AppendEvent pipeline.
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("flusher did not write the audit row to the events table through the Start lifecycle")
}

// TestAuditLifecycle_CloseDrainsInFlightRows asserts that calling
// srv.audit.Close() drains any rows still buffered in the
// channel — the daemon shutdown path leaves the events table
// consistent with the in-process queue (issue #286). Enqueues
// several rows via the full server path, then calls Close and
// asserts every row landed in the store.
func TestAuditLifecycle_CloseDrainsInFlightRows(t *testing.T) {
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid")
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
		WithOpsMetrics(context.Background(), ops)
	h := srv.Handler()

	// Drive several failed-login attempts through the full
	// pipeline so the channel has buffered rows at Close time.
	const n = 5
	for i := 0; i < n; i++ {
		form := url.Values{
			"email":    {"victim@example.com"},
			"password": {"any-password-1234567890"},
		}
		req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "203.0.113.7:54321"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("/login status = %d, want 401", rec.Code)
		}
	}
	(*

	// Close drains the channel. After Close returns, every row
	// must have landed in the events table.
	srv.Audit()).Close()

	events, err := store.ListEvents(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var got int
	for _, e := range events {
		if e.Kind == apidserver.KindAuthLoginFailed {
			got++
		}
	}
	if got != n {
		t.Errorf("after Close, %d auth.login.failed rows in events table, want %d (shutdown drain incomplete)", got, n)
	}
}

// TestAuditLifecycle_FailingStoreIncrementsDedicatedCounter
// asserts the load-bearing contract from review fix #3: when the
// underlying AppendEvent fails, the dedicated
// failedLoginAuditWriteFailures counter is incremented and the
// success-path auditWriteFailures counter is NOT. Routing the
// failure through the success-path counter would collapse the
// nil subject into account_id="anonymous" and conflate with
// legitimate anonymous-success-path failures.
func TestAuditLifecycle_FailingStoreIncrementsDedicatedCounter(t *testing.T) {
	store := failingAppendStore{Store: state.NewMemStore()}
	ops := wire.NewOpsMetrics("apid")
	srv := apidserver.NewServer(statetest.NewStore(store), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
		WithOpsMetrics(context.Background(), ops)
	defer (*srv.Audit()).Close()

	h := srv.Handler()
	form := url.Values{"email": {"victim@example.com"}, "password": {"any-password-1234567890"}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.7:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// 401 still returns — the audit-write failure must not block
	// the customer response.
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/login status = %d, want 401", rec.Code)
	}

	// Wait for the flusher to attempt + fail AppendEvent.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		// The dedicated counter has incremented.
		if c := ops.FailedLoginAuditWriteFailures(); c != nil {
			if got := testutil.ToFloat64(c); got == 1 {
				// And the success-path anonymous counter remains untouched.
				if got := testutil.ToFloat64(ops.AuditWriteFailures("anonymous")); got != 0 {
					t.Errorf("success-path auditWriteFailures counter incremented on the failed-login path: anonymous=%v (review fix #3 violated)", got)
				}
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("dedicated failedLoginAuditWriteFailures counter did not increment")
}
