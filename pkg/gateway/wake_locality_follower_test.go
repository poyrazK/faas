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

// blockingAdmitBackend holds the gate leader's admission until release
// closes, so a second request can join the wake in flight.
type blockingAdmitBackend struct {
	*fakeBackend
	admitting chan struct{}
	release   chan struct{}
}

func (b *blockingAdmitBackend) Admit(ctx context.Context, app, deployment, scope, trigger string, maximum int) (string, WakeMethod, bool, error) {
	close(b.admitting)
	select {
	case <-b.release:
	case <-ctx.Done():
		return "", WakeMethodUnspecified, false, ctx.Err()
	}
	return b.fakeBackend.Admit(ctx, app, deployment, scope, trigger, maximum)
}

// TestWakeMetricsCountOncePerWakeAndEveryCaller pins the split between the
// per-wake metrics (the §12 wake SLO, cold boots, locality) and the
// per-request one. Two requests share one wake: the leader admits it, the
// other joins it in flight. On the pinned path the joiner used to be counted
// as a second wake and filed as local_coldboot, so a 2026-10-31 load test
// whose 15 wakes were all restores reported 8,497 cold boots.
func TestWakeMetricsCountOncePerWakeAndEveryCaller(t *testing.T) {
	for _, pinned := range []bool{true, false} {
		name := "unpinned"
		if pinned {
			name = "pinned public routing"
		}
		t.Run(name, func(t *testing.T) {
			h, fixture, _ := newTestHandler(t)
			admitting, release := make(chan struct{}), make(chan struct{})
			if pinned {
				deployment := uuid.NewString()
				backend := &publicSnapshotBackend{fakeBackend: fixture}
				h.backend = backend
				h.WithPublicRoutingPolicy(func(ctx context.Context, app App, inputs PublicRoutingInputs) (PublicRoutingSnapshot, error) {
					snapshot := publicSnapshotFixture(app, inputs)
					snapshot.Weights = []DeploymentWeightsRow{{ID: deployment, TrafficPercent: 100}}
					return snapshot, nil
				})
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
			} else {
				h.backend = &blockingAdmitBackend{fakeBackend: fixture, admitting: admitting, release: release}
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

			for needle, want := range map[string]int{
				`gateway_wake_locality_total{outcome="local_coldboot"}`: 1,
				`gateway_wake_locality_total{outcome="local_snapshot"}`: 0,
				"gateway_wake_latency_seconds_count ":                   1,
				"gateway_wake_request_latency_seconds_count ":           2,
				`gateway_cold_boot_total{app=`:                          1,
			} {
				if got := counterValueFromBody(t, h.metrics, needle); got != want {
					t.Errorf("%s = %d, want %d", needle, got, want)
				}
			}
		})
	}
}
