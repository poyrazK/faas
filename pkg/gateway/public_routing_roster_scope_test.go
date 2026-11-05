package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// A project app still serving from the legacy default scope gets its roster
// from that scope, and the selected deployment is admitted there, as the
// weighted picker's wakes were. A roster scope that is not the other
// production lane is refused.
func TestPublicRoutingAdmitsInTheRosterScope(t *testing.T) {
	for _, tc := range []struct {
		name, roster, wantScope string
		project                 bool
		wantStatus              int
	}{
		{"project app on the legacy default scope", "default", "default", true, http.StatusNoContent},
		{"project app on production", "", "production", true, http.StatusNoContent},
		{"unscoped app on named production", "production", "production", false, http.StatusNoContent},
		{"unscoped app claiming its own lane", "default", "", false, http.StatusServiceUnavailable},
		{"project app claiming a stage", "staging", "", true, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, backend, _ := newTestHandler(t)
			deployment := uuid.NewString()
			backend.mu.Lock()
			if tc.project {
				backend.app.ProjectID = uuid.NewString()
			}
			backend.mu.Unlock()
			wrapped := &publicSnapshotBackend{fakeBackend: backend}
			h.backend = wrapped
			h.WithPublicRoutingPolicy(func(_ context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
				snapshot := publicSnapshotFixture(app, inputs)
				snapshot.Weights, snapshot.RosterScope = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}, tc.roster
				if inputs.ResolveRelease {
					snapshot.ReleaseVerdict = "allowed"
				}
				return snapshot, nil
			})
			admitted := ""
			wrapped.onAdmit = func(_ context.Context, id, scope string, _ int) error {
				admitted = scope
				backend.mu.Lock()
				defer backend.mu.Unlock()
				backend.targets = []Target{{InstanceID: "one", NodeID: "node", DeploymentID: id}}
				return nil
			}
			h.WithForwarding(func(Target) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+backend.host+"/", nil))
			if rec.Code != tc.wantStatus || admitted != tc.wantScope {
				t.Fatalf("status=%d admitted scope=%q, want %d %q; body=%s", rec.Code, admitted, tc.wantStatus, tc.wantScope, rec.Body)
			}
		})
	}
}
