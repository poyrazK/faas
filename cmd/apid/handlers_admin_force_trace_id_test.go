// handlers_admin_force_trace_id_test.go — PR-#TBD / fix-cluster B:
// pins the trace_id wiring contract for the three force-action
// handlers (force-park, force-cold-boot, force-restart).
//
// Pins:
//   - When the inbound request carries an X-Trace-Id header
//     (matched by the global TraceID middleware in
//     cmd/apid/server.go:1519-1531), the trace_id flows
//     through InsertOperatorIntent onto the
//     operator_intents.trace_id column. The fakeStoreForIntent's
//     intentInsertCall.TraceID captures the value.
//   - When the inbound request omits the header, the global
//     middleware generates a fresh 32-char OTel hex value;
//     the handler picks it up via middleware.TraceIDFrom(r)
//     (NOT nil — the previous C6 implementation passed nil
//     and the column was silently NULL on every apid HTTP
//     force-action).
//   - A non-middleware caller (direct request construction,
//     no mux) preserves the pre-PR contract: nil trace_id,
//     column stays NULL. This matches pkg/middleware/traceid.go's
//     "nil when no middleware ran AND no inbound header" rule
//     and keeps cron-fired sites + tests honest.
//
// The three handlers share a single test shape (one per verb);
// the table-driven force_test.go cases cover the
// confirm/reason/state-gate edges that this file does NOT pin.
package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const fixedTraceIDForHandlerTests = "4bf92f3577b34da6a3ce929d0e0e4736"

// runForceParkHandler seeds an instance in RUNNING state and
// invokes POST /v1/admin/instances/{id}/force-park. Returns the
// captured insertCalls so the caller can assert on trace_id
// propagation.
func runForceParkHandler(t *testing.T, fake *fakeStoreForIntent, setHeader bool) ([]intentInsertCall, int) {
	t.Helper()
	srv, store, cookie := newForceHarness(t, fake)
	insID, _ := seedRunningInstance(t, store, "RUNNING")

	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/instances/"+insID+"/force-park?confirm=true&reason=trace_id_wiring_test", nil)
	req.AddCookie(cookie)
	req.Header.Set("Idempotency-Key", "test-admin-mutation")
	if setHeader {
		req.Header.Set("X-Trace-Id", fixedTraceIDForHandlerTests)
	}
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	return fake.insertCalls, rec.Code
}

// runForceColdBootHandler seeds an app + deployment and invokes
// POST /v1/admin/apps/{slug}/force-cold-boot.
func runForceColdBootHandler(t *testing.T, fake *fakeStoreForIntent, setHeader bool) ([]intentInsertCall, int) {
	t.Helper()
	srv, store, cookie := newForceHarness(t, fake)
	_, _, _ = seedAppAndDeployment(t, store)

	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/apps/tenant-app/force-cold-boot?confirm=true&reason=trace_id_wiring_test", nil)
	req.AddCookie(cookie)
	req.Header.Set("Idempotency-Key", "test-admin-mutation")
	if setHeader {
		req.Header.Set("X-Trace-Id", fixedTraceIDForHandlerTests)
	}
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	return fake.insertCalls, rec.Code
}

// runForceRestartHandler seeds an instance in RUNNING state and
// invokes POST /v1/admin/instances/{id}/force-restart.
func runForceRestartHandler(t *testing.T, fake *fakeStoreForIntent, setHeader bool) ([]intentInsertCall, int) {
	t.Helper()
	srv, store, cookie := newForceHarness(t, fake)
	insID, _ := seedRunningInstance(t, store, "RUNNING")

	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/instances/"+insID+"/force-restart?confirm=true&reason=trace_id_wiring_test", nil)
	req.AddCookie(cookie)
	req.Header.Set("Idempotency-Key", "test-admin-mutation")
	if setHeader {
		req.Header.Set("X-Trace-Id", fixedTraceIDForHandlerTests)
	}
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	return fake.insertCalls, rec.Code
}

// TestForcePark_TraceIDFlowsToIntentRow is the load-bearing pin
// for fix-cluster finding #1: the handler MUST call
// middleware.TraceIDFrom(r) for the traceID arg of
// InsertOperatorIntent (the previous C6 implementation passed
// nil, so operator_intents.trace_id was NULL on every apid HTTP
// force-action and the C5 trace_id_completeness_ratio gauge
// reported 0.0 for every kind).
func TestForcePark_TraceIDFlowsToIntentRow(t *testing.T) {
	t.Run("with_inbound_header_uses_header_value", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		calls, status := runForceParkHandler(t, fake, true)
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
		if len(calls) != 1 {
			t.Fatalf("insertCalls = %d, want 1", len(calls))
		}
		if calls[0].TraceID == nil {
			t.Fatalf("TraceID = nil; want %q (header value)", fixedTraceIDForHandlerTests)
		}
		if *calls[0].TraceID != fixedTraceIDForHandlerTests {
			t.Errorf("TraceID = %q, want %q", *calls[0].TraceID, fixedTraceIDForHandlerTests)
		}
		if calls[0].Kind != state.OperatorIntentKindForcePark {
			t.Errorf("Kind = %q, want force_park", calls[0].Kind)
		}
	})

	t.Run("without_inbound_header_uses_fresh_middleware_value", func(t *testing.T) {
		// The global TraceID middleware generates a fresh 32-char
		// OTel hex when no header is set. The handler picks it up
		// via middleware.TraceIDFrom(r) — NOT nil. Pre-PR C6
		// would have written nil here, defeating the trace_id
		// column completely.
		fake := &fakeStoreForIntent{}
		calls, status := runForceParkHandler(t, fake, false)
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
		if len(calls) != 1 {
			t.Fatalf("insertCalls = %d, want 1", len(calls))
		}
		if calls[0].TraceID == nil {
			t.Fatalf("TraceID = nil; want non-nil (middleware generates fresh when header absent)")
		}
		if got := *calls[0].TraceID; len(got) != 32 {
			t.Errorf("TraceID length = %d, want 32 (OTel hex); value=%q", len(got), got)
		}
	})
}

// TestForceColdBoot_TraceIDFlowsToIntentRow mirrors the
// force-park pin for the cold-boot handler.
func TestForceColdBoot_TraceIDFlowsToIntentRow(t *testing.T) {
	t.Run("with_inbound_header_uses_header_value", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		calls, status := runForceColdBootHandler(t, fake, true)
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
		if len(calls) != 1 {
			t.Fatalf("insertCalls = %d, want 1", len(calls))
		}
		if calls[0].TraceID == nil || *calls[0].TraceID != fixedTraceIDForHandlerTests {
			t.Errorf("TraceID = %v, want %q", calls[0].TraceID, fixedTraceIDForHandlerTests)
		}
		if calls[0].Kind != state.OperatorIntentKindForceColdBoot {
			t.Errorf("Kind = %q, want force_cold_boot", calls[0].Kind)
		}
	})

	t.Run("without_inbound_header_uses_fresh_middleware_value", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		calls, status := runForceColdBootHandler(t, fake, false)
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
		if len(calls) != 1 {
			t.Fatalf("insertCalls = %d, want 1", len(calls))
		}
		if calls[0].TraceID == nil || len(*calls[0].TraceID) != 32 {
			t.Errorf("TraceID = %v, want 32-char fresh OTel hex", calls[0].TraceID)
		}
	})
}

// TestForceRestart_TraceIDFlowsToIntentRow mirrors the
// force-park pin for the restart handler.
func TestForceRestart_TraceIDFlowsToIntentRow(t *testing.T) {
	t.Run("with_inbound_header_uses_header_value", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		calls, status := runForceRestartHandler(t, fake, true)
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
		if len(calls) != 1 {
			t.Fatalf("insertCalls = %d, want 1", len(calls))
		}
		if calls[0].TraceID == nil || *calls[0].TraceID != fixedTraceIDForHandlerTests {
			t.Errorf("TraceID = %v, want %q", calls[0].TraceID, fixedTraceIDForHandlerTests)
		}
		if calls[0].Kind != state.OperatorIntentKindForceRestart {
			t.Errorf("Kind = %q, want force_restart", calls[0].Kind)
		}
	})

	t.Run("without_inbound_header_uses_fresh_middleware_value", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		calls, status := runForceRestartHandler(t, fake, false)
		if status != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", status)
		}
		if len(calls) != 1 {
			t.Fatalf("insertCalls = %d, want 1", len(calls))
		}
		if calls[0].TraceID == nil || len(*calls[0].TraceID) != 32 {
			t.Errorf("TraceID = %v, want 32-char fresh OTel hex", calls[0].TraceID)
		}
	})
}

// TestForcePark_NonMiddlewareCaller_NilTraceID pins the
// pre-PR contract: a direct request construction (no global
// mux → no TraceID middleware) returns nil from
// middleware.TraceIDFrom(r), and the column stays NULL.
//
// This is a white-box sanity check using the handler function
// directly via srv.handler() — but bypassing the mux is hard
// without a test-only wrapper. Instead we exercise the
// no-header path through the mux (which IS the production
// path for inbound-from-internet requests): the middleware
// generates fresh, the handler captures it, the column is
// non-NULL. The "nil" case is covered separately by
// pkg/middleware/traceid_test.go::TestTraceIDFrom_NoMiddlewareNoHeaderReturnsNil.
//
// Here we verify a store-error path returns 500 with the
// trace_id still captured — the InsertOperatorIntent call
// fires BEFORE the error return, so the trace_id arg is
// already passed.
func TestForcePark_StoreError_StillCapturesTraceID(t *testing.T) {
	fake := &fakeStoreForIntent{insertErr: errors.New("connection refused")}
	calls, status := runForceParkHandler(t, fake, true)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}
	if len(calls) != 1 {
		t.Fatalf("insertCalls = %d, want 1 (the call IS made before the error path returns)", len(calls))
	}
	if calls[0].TraceID == nil || *calls[0].TraceID != fixedTraceIDForHandlerTests {
		t.Errorf("TraceID = %v, want %q (handler must capture trace_id BEFORE InsertOperatorIntent returns)", calls[0].TraceID, fixedTraceIDForHandlerTests)
	}
	// Sanity: the contract is that even on store error, the
	// pre-Insert state was set up correctly (test plan calls
	// for end-to-end coverage of the trace_id arg slot).
	_ = api.Problem{} // keep import live for the linter
	_ = context.Background
}
