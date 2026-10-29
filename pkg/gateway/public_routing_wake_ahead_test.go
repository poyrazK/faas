package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// wakeAheadSnapshotBackend reports only the woken app's targets as healthy,
// so its declared dependency still looks parked to wake-ahead.
type wakeAheadSnapshotBackend struct{ *publicSnapshotBackend }

func (b wakeAheadSnapshotBackend) HealthyCount(appID string) int {
	if appID != b.app.ID {
		return 0
	}
	return b.publicSnapshotBackend.HealthyCount(appID)
}

// ADR-977 on the pinned public path (ADR-570): a public request that wakes a
// parked app through its routing snapshot starts restoring the services the
// app declares, before its own wake finishes.
func TestPublicRoutingColdWakeStartsWakeAhead(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	deployment := uuid.NewString()
	backend := &publicSnapshotBackend{fakeBackend: fixture}
	h.backend = wakeAheadSnapshotBackend{backend}
	h.WithPublicRoutingPolicy(func(_ context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
		return snapshot, nil
	})
	backend.onAdmit = func(context.Context, string, string, int) error {
		fixture.AddTarget(Target{InstanceID: "woken", DeploymentID: deployment, NodeID: "node"})
		return nil
	}
	woken := make(chan string, 1)
	learner, _, _ := newTestLearner()
	h.SetWakeAhead(WakeAheadConfig{
		Learner:   learner,
		Enabled:   func(context.Context, string) (bool, error) { return true, nil },
		Residency: func(context.Context) (int64, int64, error) { return 0, 10000, nil },
		Declared: func(_ context.Context, appID string) ([]string, error) {
			if appID == fixture.app.ID {
				return []string{"ledger"}, nil
			}
			return nil, nil
		},
		Wake: func(_ context.Context, appID string) error {
			woken <- appID
			return nil
		},
	})
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cold public request = %d/%s", rec.Code, rec.Body)
	}
	select {
	case target := <-woken:
		if target != "ledger" {
			t.Fatalf("woke %q ahead, want the declared ledger", target)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cold public wake did not wake its declared dependency ahead")
	}
}
