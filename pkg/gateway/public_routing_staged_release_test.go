// adr: 20261031105227 — a release staged with no traffic must not keep a
// parked serving release from waking (#6121).
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// stagedReleaseBackend gives the snapshot backend the per-deployment instance
// counts the production PGBackend has.
type stagedReleaseBackend struct{ *publicSnapshotBackend }

func (b stagedReleaseBackend) CapacityCount(string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.targets)
}

func (b stagedReleaseBackend) CapacityCountForDeployment(_, deployment string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, target := range b.targets {
		if target.DeploymentID == deployment {
			n++
		}
	}
	return n
}

func TestPublicRoutingServingReleaseWakesBesideStagedRelease(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	fixture.app.MaxConcurrency = 1
	serving, staged := uuid.NewString(), uuid.NewString()
	// The staged release has 0% traffic, so it is not in the weights, and it
	// holds the app's only instance; the serving release is parked.
	fixture.AddTarget(Target{InstanceID: "staged", DeploymentID: staged, NodeID: "node"})
	backend := stagedReleaseBackend{&publicSnapshotBackend{fakeBackend: fixture}}
	h.backend = backend
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = []DeploymentWeightsRow{{ID: serving, TrafficPercent: 100}}
		return snapshot, nil
	})
	backend.onAdmit = func(ctx context.Context, deployment, scope string, maximum int) error {
		if deployment != serving || maximum != 1+api.RolloutConcurrencyGrant {
			return fmt.Errorf("serving wake beside a staged release: %s/%d", deployment, maximum)
		}
		fixture.AddTarget(Target{InstanceID: "serving", DeploymentID: serving, NodeID: "node"})
		return nil
	}
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if target.DeploymentID != serving {
				t.Errorf("production reached the staged release: %s", target.DeploymentID)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("serving release beside a staged one = %d/%s", rec.Code, rec.Body)
	}
}

func TestPublicRoutingWakeMaximumGrantsOnlyBesideAnotherRelease(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []string
		want    int
	}{
		{"staged release holds the only instance", []string{"staged"}, 1 + api.RolloutConcurrencyGrant},
		{"serving release already runs", []string{"serving", "staged"}, 1},
		{"no instance at all", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, fixture, _ := newTestHandler(t)
			fixture.app.MaxConcurrency = 1
			for _, deployment := range tc.targets {
				fixture.AddTarget(Target{InstanceID: deployment + "-1", DeploymentID: deployment, NodeID: "node"})
			}
			h.backend = stagedReleaseBackend{&publicSnapshotBackend{fakeBackend: fixture}}
			routing := PublicRoutingSnapshot{SelectedDeploymentID: "serving", Scope: "production", SelectionReason: "weighted",
				Weights: []DeploymentWeightsRow{{ID: "serving", TrafficPercent: 100}}}
			if got := h.publicRoutingWakeMaximum(fixture.app, routing); got != tc.want {
				t.Fatalf("wake maximum = %d, want %d", got, tc.want)
			}
		})
	}
}
