package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

// Hunt 22 H22-1: the detached leader ran under the gate TTL alone. An app's
// longer wait budget could not take effect, and a wake schedd was still
// allowed to finish ended every waiter with a failure instead of "wake in
// progress".
func TestWakeGateLeaderOutlivesTheTTL(t *testing.T) {
	const ttl = 50 * time.Millisecond
	for _, tc := range []struct {
		name        string
		floor, wait time.Duration
		// waiterTimesOut is whether the caller's own budget ends before the
		// wake does; the generation must still be in progress then.
		waiterTimesOut bool
	}{
		{name: "app wait budget above the TTL", wait: 2 * time.Second},
		{name: "scheduler floor above the TTL and the wait", floor: 2 * time.Second, wait: ttl, waiterTimesOut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWakeGate(8, ttl)
			g.leaderFloor = tc.floor
			policy := WakeAdmissionPolicy{MaxWaiters: 8, MaxWait: tc.wait, Priority: 1}
			release := make(chan struct{})
			ensured := make(chan error, 1)
			returned := make(chan error, 1)
			go func() {
				returned <- g.WaitWithPolicy(context.Background(), "app", "acct", policy, func() bool { return true }, func(ctx context.Context) error {
					select {
					case <-release:
						ensured <- nil
						return nil
					case <-ctx.Done():
						ensured <- ctx.Err()
						return ctx.Err()
					}
				}, nil, nil)
			}()
			if tc.waiterTimesOut {
				var timeout *WakeQueueWaitTimeoutError
				if err := <-returned; !errors.As(err, &timeout) {
					t.Fatalf("waiter error = %v, want its wait budget to expire", err)
				}
			}
			time.Sleep(2 * ttl)
			if !g.WakeInProgress("app") {
				t.Fatal("the wake generation ended at the gate TTL")
			}
			close(release)
			if err := <-ensured; err != nil {
				t.Fatalf("ensure ended with %v, want the wake to finish", err)
			}
			if !tc.waiterTimesOut {
				if err := <-returned; err != nil {
					t.Fatalf("waiter error = %v, want the finished wake", err)
				}
			}
		})
	}
}

func TestWakeGateLeaderBudget(t *testing.T) {
	g := NewWakeGate(8, 30*time.Second)
	for _, tc := range []struct {
		floor, wait, want time.Duration
	}{
		{wait: 10 * time.Second, want: 30 * time.Second},
		{wait: 60 * time.Second, want: 60 * time.Second},
		{floor: 46 * time.Second, wait: 30 * time.Second, want: 46 * time.Second},
		{floor: 46 * time.Second, wait: 120 * time.Second, want: 120 * time.Second},
	} {
		g.leaderFloor = tc.floor
		if got := g.leaderBudget(WakeAdmissionPolicy{MaxWait: tc.wait}); got != tc.want {
			t.Errorf("leaderBudget(floor %v, wait %v) = %v, want %v", tc.floor, tc.wait, got, tc.want)
		}
	}
}

// The handler's gate never ends a wake before schedd's own leader would.
func TestHandlerWakeGateCoversTheSchedulerLeaderBudget(t *testing.T) {
	h := NewHandlerWith(&fakeBackend{}, NewMetrics(), nil)
	if got := h.gate.leaderBudget(WakeAdmissionPolicyForPlan(api.PlanPro)); got <= sched.DefaultWakeLeaderBudget {
		t.Fatalf("gate leader budget = %v, want more than schedd's %v", got, sched.DefaultWakeLeaderBudget)
	}
}
