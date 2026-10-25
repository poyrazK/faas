// adr: 570
package gateway

import (
	"context"
	"testing"
	"time"
)

type capturedCapacityBackend struct {
	*PGBackend
	started, release chan struct{}
	deployment       string
	maximum, count   int
}

func (b *capturedCapacityBackend) AdmitDeploymentBurst(ctx context.Context, app, deployment, scope, trigger string, maximum, count int) (int, error) {
	b.deployment, b.maximum, b.count = deployment, maximum, count
	close(b.started)
	select {
	case <-b.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	b.RecordTarget(app, Target{InstanceID: "admitted", DeploymentID: deployment, NodeID: "node"})
	return 1, nil
}

func TestPublicRoutingBurstCountsCapturedCohortsWithoutPickerWeights(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "empty picker"
		if stale {
			name = "retired weighted residents"
		}
		t.Run(name, func(t *testing.T) {
			h, fixture, _ := newTestHandler(t)
			store := serviceDiscoveryWeightStore{}
			if stale {
				store.rows = []DeploymentWeightsRow{{ID: "retired", TrafficPercent: 100}}
			}
			b := &capturedCapacityBackend{PGBackend: NewPGBackend(nil, nil, nil).WithStore(store), started: make(chan struct{}), release: make(chan struct{})}
			if err := b.RefreshDeploymentWeights(t.Context(), fixture.app.ID); err != nil {
				t.Fatal(err)
			}
			if stale {
				b.RecordTarget(fixture.app.ID, Target{InstanceID: "retired", DeploymentID: "retired", NodeID: "node"})
			}
			h.backend = b
			state := h.burstPressure.state(fixture.app.ID)
			state.inflight.Store(1)
			defer state.inflight.Store(0)
			rows := []DeploymentWeightsRow{{ID: "selected", TrafficPercent: 100}}
			routing := PublicRoutingSnapshot{SelectedDeploymentID: "selected", Scope: "production", SelectionReason: "weighted", Weights: rows}
			done := make(chan error, 1)
			go func() {
				waited, err := h.maybePublicRoutingBurst(t.Context(), fixture.app, 2, 1, routing)
				if !waited {
					t.Error("retired or empty picker satisfied captured capacity")
				}
				done <- err
			}()
			select {
			case <-b.started:
			case <-time.After(time.Second):
				t.Fatal("captured cohort was not admitted")
			}
			// A later policy edit cannot change the worker's capacity roster.
			rows[0].ID = "retired"
			close(b.release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("ready admission was mistaken for no progress")
			}
			if b.deployment != "selected" || b.maximum != 2 || b.count != 1 || !b.PickForDeployment(fixture.app.ID, "selected").OK {
				t.Fatalf("admission changed cohort/cap: %s/%d/%d", b.deployment, b.maximum, b.count)
			}
			wantWeighted := 0
			if stale {
				wantWeighted = 1
			}
			if got := b.HealthyCount(fixture.app.ID); got != wantWeighted {
				t.Fatalf("captured admission rewrote picker weights: healthy=%d", got)
			}
		})
	}
}

func TestPublicRoutingCapacityRetainsReadinessAndAppIsolation(t *testing.T) {
	b := NewPGBackend(nil, nil, nil).WithStore(serviceDiscoveryWeightStore{})
	for _, target := range []Target{
		{InstanceID: "one", DeploymentID: "first", NodeID: "node"},
		{InstanceID: "two", DeploymentID: "first", NodeID: "node"},
		{InstanceID: "withdrawn", DeploymentID: "first", NodeID: "node", RequiresReadiness: true},
		{InstanceID: "second", DeploymentID: "second", NodeID: "node"},
		{InstanceID: "stage", DeploymentID: "stage", NodeID: "node"},
	} {
		b.RecordTarget("app", target)
	}
	b.RecordTarget("foreign", Target{InstanceID: "foreign", DeploymentID: "first", NodeID: "node"})
	b.SetInstanceReadiness("app", "withdrawn", "unready", time.Now(), 1)
	if got := b.HealthyCountForDeployments("app", []string{"first", "second", "first", "absent"}); got != 3 || b.CapacityCount("app") != 5 || b.HealthyCount("app") != 0 {
		t.Fatalf("captured capacity crossed app/roster/readiness or lost residents: got=%d capacity=%d weighted=%d", got, b.CapacityCount("app"), b.HealthyCount("app"))
	}
	if b.HealthyCountForDeployments("app", nil) != 0 || b.HealthyCountForDeployments("absent", []string{"first"}) != 0 {
		t.Fatal("empty or unknown app inherited eligible capacity")
	}
	// Count-only inspection must not make the legacy weighted picker routable.
	if b.Pick("app").OK {
		t.Fatal("count installed mutable deployment weights")
	}
}
