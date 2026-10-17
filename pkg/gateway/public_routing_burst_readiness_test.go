// adr: 570
package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPublicRoutingBurstWaitsForCapturedCohortReadiness(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	b := &capturedCapacityBackend{
		PGBackend: NewPGBackend(nil, nil, nil).WithStore(serviceDiscoveryWeightStore{
			rows: []DeploymentWeightsRow{{ID: "retired", TrafficPercent: 100}},
		}),
		started: make(chan struct{}), release: make(chan struct{}),
	}
	if err := b.RefreshDeploymentWeights(t.Context(), fixture.app.ID); err != nil {
		t.Fatal(err)
	}
	b.RecordTarget(fixture.app.ID, Target{InstanceID: "retired", DeploymentID: "retired", NodeID: "node"})
	h.backend = b
	h.burstPressure.state(fixture.app.ID).inflight.Store(1)
	var once sync.Once
	release := func() { once.Do(func() { close(b.release) }) }
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	routing := PublicRoutingSnapshot{SelectedDeploymentID: "selected", Scope: "production", SelectionReason: "weighted", Weights: []DeploymentWeightsRow{{ID: "selected", TrafficPercent: 100}}}
	done := make(chan error, 1)
	go func() {
		_, err := h.maybePublicRoutingBurst(ctx, fixture.app, 2, 1, routing)
		done <- err
	}()
	select {
	case <-b.started:
	case <-ctx.Done():
		t.Fatal("captured cohort admission did not start")
	}
	select {
	case err := <-done:
		t.Fatalf("retired picker resident released captured admission: %v", err)
	case <-time.After(2 * routableTargetPollInterval):
	}
	release()
	select {
	case err := <-done:
		if err != nil || !b.PickForDeployment(fixture.app.ID, "selected").OK {
			t.Fatalf("captured cohort did not become routable: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("captured cohort readiness did not release request")
	}
}

type stalledCapturedCapacityBackend struct{ *PGBackend }

func (b *stalledCapturedCapacityBackend) AdmitDeploymentBurst(context.Context, string, string, string, string, int, int) (int, error) {
	return 0, nil
}

func TestPublicRoutingBurstStallRejectsUncapturedReadiness(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	b := &stalledCapturedCapacityBackend{NewPGBackend(nil, nil, nil).WithStore(serviceDiscoveryWeightStore{
		rows: []DeploymentWeightsRow{{ID: "retired", TrafficPercent: 100}},
	})}
	if err := b.RefreshDeploymentWeights(t.Context(), fixture.app.ID); err != nil {
		t.Fatal(err)
	}
	b.RecordTarget(fixture.app.ID, Target{InstanceID: "retired", DeploymentID: "retired", NodeID: "node"})
	h.backend = b
	h.burstPressure.state(fixture.app.ID).inflight.Store(1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*routableTargetPollInterval)
	defer cancel()
	routing := PublicRoutingSnapshot{SelectedDeploymentID: "selected", Scope: "production", SelectionReason: "weighted", Weights: []DeploymentWeightsRow{{ID: "selected", TrafficPercent: 100}}}
	_, err := h.maybePublicRoutingBurst(ctx, fixture.app, 2, 1, routing)
	if err == nil || b.PickForDeployment(fixture.app.ID, "selected").OK || !errors.Is(err, errBurstCapacityStalled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("uncaptured resident hid stalled captured admission: %v", err)
	}
}
