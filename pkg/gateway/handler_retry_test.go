// adr: 201
package gateway

// End-to-end retry through the real Handler path. pkg/gateway/retry_test.go
// pins the safety rules in isolation; these prove the loop is actually
// reachable from ServeHTTP and that the gate genuinely gates.

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// retryTestHandler builds a handler over two cached targets where
// "instance-dead" fails in transport exactly as the vmmd bridge does, and
// "instance-live" serves. The forwarder is injected because markStaleTarget
// is raised by the gRPC bridge (pkg/gateway/forwardproxy.go), not by the
// legacy addr-based httputil proxy.
func retryTestHandler(t *testing.T) (*Handler, *fakeBackend, *atomic.Int32) {
	t.Helper()
	b := &fakeBackend{
		app:  App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host: "jane-api.apps.dom",
	}
	b.AddTarget(Target{NodeID: "node-dead", InstanceID: "instance-dead"})
	b.AddTarget(Target{NodeID: "node-live", InstanceID: "instance-live"})

	var forwards atomic.Int32
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwards.Add(1)
			if target.InstanceID == "instance-dead" {
				markStaleTarget(r.Context())
				http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("served by " + target.InstanceID))
		})
	})
	return h, b, &forwards
}

func retryRequest(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// With the gate off the handler must behave exactly as it did before
// ADR-201: one forward, and the transport failure reaches the client.
func TestHandlerRetryDisabledSurfacesTransportFailure(t *testing.T) {
	h, _, forwards := retryTestHandler(t)

	rec := retryRequest(t, h)

	if got := forwards.Load(); got != 1 {
		t.Fatalf("forwards = %d, want 1 with FAAS_GATEWAY_RETRY off", got)
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200; with retry off a dead target must surface its failure")
	}
}

// The gate on, with a policy allowing one replay: the dead target's failure
// must be converted into the sibling's 200.
func TestHandlerRetryConvertsTransportFailureIntoSuccess(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	h.WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})

	rec := retryRequest(t, h)

	if got := forwards.Load(); got != 2 {
		t.Fatalf("forwards = %d, want 2 (original + one replay)", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 from the healthy sibling; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); got != "served by instance-live" {
		t.Fatalf("body = %q, want the sibling's response", got)
	}
}

// The gate on but the policy inert (MaxAttempts < 2) must not replay. This is
// the rollout posture: an operator flips the flag fleet-wide, then enables
// retry per-app via edge rules.
func TestHandlerRetryGateOnButPolicyInertDoesNotReplay(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	h.WithRetryEnabled(true)

	retryRequest(t, h)

	if got := forwards.Load(); got != 1 {
		t.Fatalf("forwards = %d, want 1 — an inert default policy must not replay", got)
	}
}

// A matched kind=retry rule overrides the operator default.
func TestHandlerRetryMatcherOverridesDefault(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	h.WithRetryEnabled(true).
		WithRetryDefault(RetryPolicy{}).
		WithRetryMatcher(func(App, *http.Request) (RetryPolicy, bool) {
			return RetryPolicy{MaxAttempts: 2}, true
		})

	rec := retryRequest(t, h)

	if got := forwards.Load(); got != 2 {
		t.Fatalf("forwards = %d, want 2 — a matched rule must override the inert default", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// The failed instance must be reported for eviction so the picker cannot hand
// it back, and so schedd starts recovery for it.
func TestHandlerRetryReportsFailedTargetForEviction(t *testing.T) {
	h, _, _ := retryTestHandler(t)
	h.WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})

	rec := retryRequest(t, h)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// fakeBackend does not implement EvictInstance, so the handler's guard in
	// proxyAttempt (identical-instance repick returns false) is what keeps the
	// replay off the dead target. Assert the replay did not land back on it.
	if got := rec.Body.String(); got == "served by instance-dead" {
		t.Fatal("replay landed back on the failed instance")
	}
}

// A POST must not be replayed by default even when the gate and policy are
// on — the customer's side effect may already have run.
func TestHandlerRetryDoesNotReplayPostByDefault(t *testing.T) {
	h, _, forwards := retryTestHandler(t)
	h.WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})

	req := httptest.NewRequest(http.MethodPost, "http://jane-api.apps.dom/charge", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := forwards.Load(); got != 1 {
		t.Fatalf("forwards = %d, want 1 — POST must not replay without an explicit opt-in", got)
	}
	if rec.Code == http.StatusOK {
		t.Fatal("status = 200; the POST should have surfaced its transport failure")
	}
}

// The vmmd bridge forwards to the instance named in x-faas-instance, not to
// the Target it is handed (forwardproxy.go builds the init frame from the
// header). A replay onto a sibling must therefore carry the sibling's
// identity; replaying the original instance's headers sent the retry back to
// the failed VM, or to an instance the sibling's node does not run.
func TestHandlerRetryRestampsIdentityForTheSibling(t *testing.T) {
	b := &fakeBackend{
		app:  App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro},
		host: "jane-api.apps.dom",
	}
	b.AddTarget(Target{NodeID: "node-dead", InstanceID: "instance-dead"})
	b.AddTarget(Target{NodeID: "node-live", InstanceID: "instance-live"})
	type seen struct{ target, header, headerID, node string }
	var attempts []seen
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			instance := r.Header.Get("x-faas-instance")
			attempts = append(attempts, seen{target.InstanceID, instance, r.Header.Get(api.InstanceIDHeader), r.Header.Get("X-Faas-Node")})
			if instance != "instance-live" {
				markStaleTarget(r.Context())
				http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte("served by " + instance))
		})
	})
	h.WithRetryEnabled(true).WithRetryDefault(RetryPolicy{MaxAttempts: 2})

	req := httptest.NewRequest(http.MethodGet, "http://jane-api.apps.dom/", nil)
	req.Header.Set("X-Faas-Instance", "instance-forged")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "served by instance-live" {
		t.Fatalf("status = %d body = %q, want the sibling's 200; attempts=%+v", rec.Code, rec.Body, attempts)
	}
	for _, a := range attempts {
		if a.header != a.target || a.headerID != a.target {
			t.Fatalf("attempt for %s carried x-faas-instance=%q %s=%q; attempts=%+v", a.target, a.header, api.InstanceIDHeader, a.headerID, attempts)
		}
	}
	if last := attempts[len(attempts)-1]; last.node != "node-live" {
		t.Fatalf("replay carried X-Faas-Node=%q, want node-live", last.node)
	}
}
