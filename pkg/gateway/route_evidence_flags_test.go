// adr: 20261028110044 — amendment 5: route evidence flags on request telemetry.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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

// A response that outlived its request budget (a WebSocket or declared
// stream) is marked long-lived, so its duration stays out of route latency.
func TestTelemetryMarksLongLivedResponses(t *testing.T) {
	for _, detached := range []bool{false, true} {
		h := &Handler{requestTelemetry: makeTestRecorder()}
		acct, app := uuid.New(), uuid.New()
		r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/events", nil), acct, app)
		r = r.WithContext(withTrafficDecision(r.Context(), false))
		recordTrafficAttempt(r.Context())
		if detached {
			recordTrafficStreamDetached(r.Context())
		}
		h.observe(r, http.StatusOK, app.String(), string(api.PlanPro), false, Target{DeploymentID: uuid.NewString()})
		rows := h.requestTelemetry.DrainBatch(4)
		if len(rows) != 1 || rows[0].LongLived != detached || rows[0].DeploymentPinned {
			t.Fatalf("detached=%v: rows %+v", detached, rows)
		}
	}
}
