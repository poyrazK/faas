// adr: 20261028110044 — amendment 5: route evidence flags on request telemetry.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

// A deployment preview or alias URL names its deployment, so its requests are
// marked; an environment URL and production routing are not.
func TestTelemetryMarksRequestsThatNameTheirDeployment(t *testing.T) {
	for _, tc := range []struct {
		name               string
		pinned, environsID string
		want               bool
	}{
		{name: "production routing"},
		{name: "deployment preview URL", pinned: "dep", want: true},
		{name: "environment URL", pinned: "dep", environsID: uuid.NewString()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deployment := uuid.NewString()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(upstream.Close)
			app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro}
			if tc.pinned != "" {
				app.PinnedDeploymentID, app.PublicEnvironmentID = deployment, tc.environsID
			}
			backend := &fakeBackend{app: app, host: "customer.example", upstream: upstream.Listener.Addr().String()}
			backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString(), DeploymentID: deployment})
			h := NewHandlerWith(backend, NewMetrics(), nil)
			h.requestTelemetry = makeTestRecorder()
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://customer.example/items", nil))
			rows := h.requestTelemetry.DrainBatch(4)
			if w.Code != http.StatusOK || len(rows) != 1 || rows[0].DeploymentPinned != tc.want {
				t.Fatalf("status %d, rows %+v; want one row with deployment_pinned=%v", w.Code, rows, tc.want)
			}
		})
	}
}

// A session (WebSocket, event stream, gRPC stream) or a detached response
// that outlived its request budget is marked long-lived, so its duration stays
// out of route latency. A streaming-enabled app's ordinary response detaches
// its budget too, but finishing within it is a request (amendment 7).
func TestTelemetryMarksLongLivedResponses(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		detached, session, outlive bool
		want                       bool
	}{
		{name: "ordinary response"},
		{name: "streamed response within its budget", detached: true},
		{name: "declared session", detached: true, session: true, want: true},
		{name: "detached response past its budget", detached: true, outlive: true, want: true},
		{name: "bounded response past its budget", outlive: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{requestTelemetry: makeTestRecorder()}
			acct, app := uuid.New(), uuid.New()
			r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/events", nil), acct, app)
			started := time.Now()
			if tc.outlive {
				started = started.Add(-time.Minute)
			}
			r = r.WithContext(reqbudget.NewContext(r.Context(), reqbudget.Budget{Total: 30 * time.Second, Started: started}))
			r = r.WithContext(withTrafficDecision(r.Context(), false))
			recordTrafficAttempt(r.Context())
			if tc.detached {
				recordTrafficStreamDetached(r.Context())
			}
			if tc.session {
				recordTrafficLongLivedSession(r.Context())
			}
			h.observe(r, http.StatusOK, app.String(), string(api.PlanPro), false, Target{DeploymentID: uuid.NewString()})
			rows := h.requestTelemetry.DrainBatch(4)
			if len(rows) != 1 || rows[0].LongLived != tc.want || rows[0].DeploymentPinned {
				t.Fatalf("rows %+v; want long_lived=%v", rows, tc.want)
			}
		})
	}
}

// A request that fails before reaching a target (a wake or capacity failure)
// is the routed deployment's evidence, so a candidate that cannot wake is
// visible to the circuit breaker, route health and first-wake rollback. A
// platform refusal stays unattributed (amendment 7).
func TestTelemetryAttributesTargetlessFailuresToTheSelectedDeployment(t *testing.T) {
	selected, served := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name    string
		target  Target
		refusal string
		want    uuid.UUID
	}{
		{name: "wake failure", want: selected},
		{name: "served response", target: Target{DeploymentID: served.String()}, want: served},
		{name: "platform refusal", refusal: "policy_unavailable", want: uuid.Nil},
		{name: "capacity refusal", refusal: "capacity", want: selected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{requestTelemetry: makeTestRecorder()}
			acct, app := uuid.New(), uuid.New()
			r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/", nil), acct, app)
			r = r.WithContext(withTrafficDecision(r.Context(), false))
			recordTrafficSelectedDeployment(r.Context(), selected.String())
			if tc.refusal != "" {
				recordTrafficRefusal(r.Context(), tc.refusal)
			}
			h.observe(r, http.StatusServiceUnavailable, app.String(), string(api.PlanPro), false, tc.target)
			rows := h.requestTelemetry.DrainBatch(4)
			if len(rows) != 1 || rows[0].DeploymentID != tc.want {
				t.Fatalf("rows %+v; want deployment %s", rows, tc.want)
			}
		})
	}
}
