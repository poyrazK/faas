package sched

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestHeartbeat_FailureThreshold pins production-us hunt #9, H9-171. A single
// ping that timed out on a starved control plane marked a healthy node
// unavailable, and the recovery arbiter then parked every instance on it
// while the VMs kept running; both nodes flapped ~22 times in 36 minutes.
// A node now fails over only after DefaultHeartbeatFailureThreshold
// consecutive failed pings, and an answered ping resets the count and runs
// the reachable hook.
func TestHeartbeat_FailureThreshold(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	dialer := &heartbeatFakeDialer{pingErr: map[string]error{node.TargetURL: errors.New("deadline exceeded while waiting for conn")}}
	var mu sync.Mutex
	var reached []string
	h := NewHeartbeat(store, dialer, nil, nil).WithReachableHook(func(_ context.Context, nodeID string) {
		mu.Lock()
		defer mu.Unlock()
		reached = append(reached, nodeID)
	})
	tick := func() state.ComputeNode {
		t.Helper()
		if err := h.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		after, err := store.ComputeNodeByID(ctx, node.ID)
		if err != nil {
			t.Fatal(err)
		}
		return after
	}
	setPingErr := func(err error) {
		dialer.mu.Lock()
		defer dialer.mu.Unlock()
		if err == nil {
			delete(dialer.pingErr, node.TargetURL)
			return
		}
		dialer.pingErr[node.TargetURL] = err
	}

	for i := 1; i < DefaultHeartbeatFailureThreshold; i++ {
		if after := tick(); !after.Active {
			t.Fatalf("failed ping %d marked the node unavailable; want it kept until %d", i, DefaultHeartbeatFailureThreshold)
		}
	}
	// An answered ping resets the count and runs the reachable hook.
	setPingErr(nil)
	if after := tick(); !after.Active {
		t.Fatal("an answered ping left the node unavailable")
	}
	mu.Lock()
	if len(reached) != 1 || reached[0] != node.ID {
		t.Fatalf("reachable hook calls = %v; want one for %s", reached, node.ID)
	}
	mu.Unlock()
	setPingErr(errors.New("deadline exceeded"))
	for i := 1; i < DefaultHeartbeatFailureThreshold; i++ {
		if after := tick(); !after.Active {
			t.Fatalf("failed ping %d after a reset marked the node unavailable", i)
		}
	}
	if after := tick(); after.Active || after.Lifecycle != state.NodeLifecycleUnavailable {
		t.Fatalf("node = active %v lifecycle %q after %d consecutive failures; want unavailable",
			after.Active, after.Lifecycle, DefaultHeartbeatFailureThreshold)
	}
}
