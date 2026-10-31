package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestPinnedWakeFollowerIsNotCountedAsAdmission pins one locality and tier
// sample per admitted wake on the pinned public-routing path. A request that
// joins another request's wake is served cold but admitted nothing; it used
// to be filed as local_coldboot, so a load test whose 15 wakes were all
// restores reported 8,497 cold boots (2026-10-31).
func TestPinnedWakeFollowerIsNotCountedAsAdmission(t *testing.T) {
	h, fixture, _ := newTestHandler(t)
	deployment := uuid.NewString()
	backend := &publicSnapshotBackend{fakeBackend: fixture}
	h.backend = backend
	h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
		snapshot := publicSnapshotFixture(app, inputs)
		snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
		return snapshot, nil
	})
	admitting := make(chan struct{})
	release := make(chan struct{})
	backend.onAdmit = func(ctx context.Context, admitted, _ string, _ int) error {
		close(admitting)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		fixture.AddTarget(Target{InstanceID: "restored", DeploymentID: admitted, NodeID: "node"})
		return nil
	}
	h.WithForwarding(func(Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	})

	var wg sync.WaitGroup
	codes := make(chan int, 2)
	serve := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://"+fixture.host+"/", nil))
			codes <- rec.Code
		}()
	}
	serve()
	select {
	case <-admitting:
	case <-time.After(5 * time.Second):
		t.Fatal("the leader never admitted a wake")
	}
	serve()
	deadline := time.Now().Add(5 * time.Second)
	for h.gate.InflightWaiters(fixture.app.ID) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("the second request did not join the wake: %d waiters", h.gate.InflightWaiters(fixture.app.ID))
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", code, http.StatusNoContent)
		}
	}

	if got := counterValueFromBody(t, h.metrics, `gateway_wake_locality_total{outcome="local_coldboot"}`); got != 1 {
		t.Fatalf("local_coldboot = %d, want 1: only the leader admitted a wake", got)
	}
	if got := counterValueFromBody(t, h.metrics, `gateway_wake_locality_total{outcome="local_snapshot"}`); got != 0 {
		t.Fatalf("local_snapshot = %d, want 0", got)
	}
}
