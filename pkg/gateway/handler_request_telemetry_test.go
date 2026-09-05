// handler_request_telemetry_test.go — proves the Handler.observe
// → recorder wiring (ADR-127) emits a row end-to-end without going
// through the gateway's ServeHTTP / fakeBackend scaffolding.
//
// Why a focused test: the existing handler_test.go suite uses
// string-shaped IDs ("app-1", "acct-1") that aren't valid UUIDs —
// perfect for the legacy observe paths, but withAppAndAccount's
// parseUUID round-trip returns the zero UUID and the recorder
// drops the row. Exercising observe directly lets the test use
// real UUIDs to assert the recorder-side state machine.

package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// makeTestRecorder constructs a production-shaped recorder with the
// kill-switch off + a quiet logger.
func makeTestRecorder() *requestTelemetryRecorder {
	return NewRequestTelemetryRecorder(RequestTelemetryConfig{
		Enabled:  true,
		RingSize: 64,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestHandlerObserveEnqueuesRow proves the wiring is end-to-end:
// Handler.observe builds a RequestTelemetryRow from r.Context()
// (account/app) + target.DeploymentID + the elapsed/cold/status
// args, then enqueues via the recorder. After observe returns,
// the recorder's ringbuffer holds one row with the expected
// fields.
func TestHandlerObserveEnqueuesRow(t *testing.T) {
	h := &Handler{requestTelemetry: makeTestRecorder()}
	acct := uuid.New()
	app := uuid.New()
	deployment := uuid.New()
	now := time.Now()
	r := httptest.NewRequest(http.MethodPost, "/checkout", nil)
	r = withAppAndAccount(r, acct, app)

	h.observe(r, 201, app.String(), string(api.PlanPro), false, Target{
		NodeID:       "n1",
		InstanceID:   "i1",
		DeploymentID: deployment.String(),
	})

	// Simulate a request that took 200ms.
	r = r.WithContext(WithStartTime(r.Context(), now.Add(-200*time.Millisecond)))
	h.observe(r, 200, app.String(), string(api.PlanPro), true, Target{
		NodeID:       "n1",
		InstanceID:   "i2",
		DeploymentID: deployment.String(),
	})

	batch := h.requestTelemetry.DrainBatch(16)
	if len(batch) != 2 {
		t.Fatalf("expected 2 rows enqueued, got %d", len(batch))
	}
	// Walk the rows; we don't assume FIFO because observe may
	// fire in a different order than the calls above (each
	// observe runs synchronously, so FIFO should hold — but
	// asserting on field values is more durable than asserting
	// on order).
	var saw201, saw200 bool
	for _, row := range batch {
		if row.AccountID != acct {
			t.Errorf("AccountID mismatch: got %v, want %v", row.AccountID, acct)
		}
		if row.AppID != app {
			t.Errorf("AppID mismatch: got %v, want %v", row.AppID, app)
		}
		if row.DeploymentID != deployment {
			t.Errorf("DeploymentID mismatch: got %v, want %v", row.DeploymentID, deployment)
		}
		if row.Route != otherRouteLabel {
			t.Errorf("route without opt-in = %q, want bounded fallback", row.Route)
		}
		if row.Method != http.MethodPost {
			t.Errorf("Method: got %q, want POST", row.Method)
		}
		switch row.Status {
		case 201:
			saw201 = true
			if row.ColdBoot {
				t.Errorf("first row: ColdBoot should be false, got true")
			}
		case 200:
			saw200 = true
			if !row.ColdBoot {
				t.Errorf("second row: ColdBoot should be true, got false")
			}
		default:
			t.Errorf("unexpected status %d", row.Status)
		}
	}
	if !saw201 || !saw200 {
		t.Fatalf("missing one of the expected status codes (saw201=%v, saw200=%v)", saw201, saw200)
	}
}

// TestHandlerObserveNoRecorderPassesThrough proves the kill-switch:
// when h.requestTelemetry is nil (unit-test seam + older call
// paths), observe does not panic and does not allocate a row.
func TestHandlerObserveNoRecorderPassesThrough(t *testing.T) {
	h := &Handler{} // requestTelemetry is nil
	// Should not panic.
	h.observe(httptest.NewRequest(http.MethodGet, "/", nil), 200, "app-1", string(api.PlanFree), false, Target{})
}

// TestHandlerObserveDropsPrePicker proves that a request without
// an account/app stamp on its context is silently dropped (the
// recorder ringbuffer stays empty). This is the contract that
// keeps 404 / pre-resolve traffic out of the telemetry table.
func TestHandlerObserveDropsPrePicker(t *testing.T) {
	h := &Handler{requestTelemetry: makeTestRecorder()}
	// No context stamps → pre-picker path.
	h.observe(httptest.NewRequest(http.MethodGet, "/", nil), 404, "", "", false, Target{})
	if got := h.requestTelemetry.PendingCount(); got != 0 {
		t.Fatalf("expected 0 rows for pre-picker request, got %d", got)
	}
}

// TestWithAppAndAccountNoOpWhenEmpty proves the stamp helper
// preserves the original request (does not allocate a new
// context) when either id is empty — important because ServeHTTP
// calls this for every request and we don't want to allocate on
// the hot path when the kill-switch is off OR the app is not
// resolved.
func TestWithAppAndAccountNoOpWhenEmpty(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	got := withAppAndAccount(r, uuid.Nil, uuid.Nil)
	if got != r {
		t.Fatalf("expected same *http.Request back when both UUIDs are zero")
	}
}

func TestHandlerTelemetryAcceptsCustomRequestIDs(t *testing.T) {
	for _, id := range []string{"burst100-caller-id", "0123456789abcdef0123456789abcdef"} {
		t.Run(id, func(t *testing.T) {
			h := &Handler{requestTelemetry: makeTestRecorder()}
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("x-faas-request-id", id)
			req = withAppAndAccount(req, uuid.New(), uuid.New())
			h.observe(req, 200, "app", string(api.PlanScale), false, Target{DeploymentID: uuid.NewString()})
			rows := h.requestTelemetry.DrainBatch(1)
			if len(rows) != 1 {
				t.Fatal("request record was dropped")
			}
			want := ""
			if len(id) == 32 {
				want = id
			}
			if rows[0].TraceID != want {
				t.Fatalf("trace ID = %q, want %q", rows[0].TraceID, want)
			}
			if req.Header.Get("x-faas-request-id") != id {
				t.Fatal("caller request ID mutated")
			}
		})
	}
}
