package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// rotatingDeploymentBackend round-robins one deployment's targets, as
// PGBackend.PickForDeploymentInstance does.
type rotatingDeploymentBackend struct {
	*fakeBackend
	next atomic.Uint64
}

func (b *rotatingDeploymentBackend) PickForDeploymentInstance(_, deploymentID, _ string) PickResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	var matching []Target
	for _, target := range b.targets {
		if target.DeploymentID == deploymentID {
			matching = append(matching, target)
		}
	}
	if len(matching) == 0 {
		return PickResult{Picked: deploymentID, ColdBucket: deploymentID}
	}
	target := matching[int((b.next.Add(1)-1)%uint64(len(matching)))]
	return PickResult{Target: target, OK: true, Picked: deploymentID}
}

// adr: 091 — amendment 2026-10-27 (GA hardening): a request pinned to one
// deployment, as every public request is once its routing is pinned
// (ADR-570), skips an instance whose circuit is open and is served by
// another instance of that deployment. The check used to skip every
// exact-deployment pick, so public traffic never consulted the breaker.
func TestHandlerCircuitAppliesToExactDeploymentPicks(t *testing.T) {
	b := &rotatingDeploymentBackend{fakeBackend: &fakeBackend{
		app:  App{ID: "app-1", AccountID: "acct-1", Plan: api.PlanPro, PinnedDeploymentID: "dep-1"},
		host: "jane-api.apps.dom",
	}}
	b.AddTarget(Target{NodeID: "node-dead", InstanceID: "instance-dead", DeploymentID: "dep-1"})
	b.AddTarget(Target{NodeID: "node-live", InstanceID: "instance-live", DeploymentID: "dep-1"})
	var forwards, dead atomic.Int32
	h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	h.WithForwarding(func(target Target) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			forwards.Add(1)
			if target.InstanceID == "instance-dead" {
				dead.Add(1)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("served by " + target.InstanceID))
		})
	})
	g := circuitTestGroup()
	h.WithCircuitBreaker(g)
	key := circuitKey("app-1", "instance-dead")
	g.Failure(key)
	g.Failure(key)

	for i := 0; i < 6; i++ {
		if rec := retryRequest(t, h); rec.Code != http.StatusOK || rec.Body.String() != "served by instance-live" {
			t.Fatalf("request %d: status=%d body=%q, want 200 from the live instance", i, rec.Code, rec.Body.String())
		}
	}
	if got := dead.Load(); got != 0 {
		t.Fatalf("the open instance received %d requests", got)
	}
	if got := forwards.Load(); got != 6 {
		t.Fatalf("forwards = %d, want 6", got)
	}
}
