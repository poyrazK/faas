// adr: 1344 — node lease takeover guard (§4, P3 phase 2).
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type fakeTakeoverGuard struct {
	allow bool
	calls map[string]int
}

func (g *fakeTakeoverGuard) MayTakeOver(_ context.Context, path, _ string) bool {
	if g.calls == nil {
		g.calls = map[string]int{}
	}
	g.calls[path]++
	return g.allow
}

// A stale heartbeat alone no longer marks a peer unavailable while the guard
// says its lease is live; once the guard allows it, the observer acts.
func TestHeartbeat_StalePeerObserverRespectsTheTakeoverGuard(t *testing.T) {
	for _, allow := range []bool{false, true} {
		ctx := context.Background()
		store := state.NewMemStore()
		owner, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		peer, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: "partitioned-peer", TargetURL: "tcp://10.0.0.22:50051",
			Lifecycle: state.NodeLifecycleActive, Active: true,
			LastHeartbeatAt: now.Add(-10 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		guard := &fakeTakeoverGuard{allow: allow}
		h := NewHeartbeat(store, &heartbeatFakeDialer{}, nil, nil).
			WithOwnerNodeID(owner.ID).
			WithStalePeerObserver(true).
			WithTakeoverGuard(guard)
		h.Staleness = time.Minute
		h.now = func() time.Time { return now }
		if err := h.Tick(ctx); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		after, err := store.ComputeNodeByID(ctx, peer.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantUnavailable := allow
		if got := after.Lifecycle == state.NodeLifecycleUnavailable; got != wantUnavailable {
			t.Fatalf("guard allow=%v: peer lifecycle %q, want unavailable=%v", allow, after.Lifecycle, wantUnavailable)
		}
		if guard.calls["peer_observer"] != 1 {
			t.Fatalf("guard consulted %d times on the peer_observer path, want 1", guard.calls["peer_observer"])
		}
	}
}

// The dead-node sweep leaves a stale-looking node's instances alone while its
// lease is live, and reads the lease once per node per tick.
func TestReconcileDeadNodeInstances_RespectsTheTakeoverGuard(t *testing.T) {
	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	guard := &fakeTakeoverGuard{allow: false}
	e.WithTakeoverGuard(guard)
	nodeID := seedStaleComputeNodeForReconcile(t, store)
	seedRunningInstance(t, store, nodeID)
	seedRunningInstance(t, store, nodeID)

	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled %d instances on a node whose lease is live", reconciled)
	}
	if guard.calls["dead_node_sweep"] != 1 {
		t.Fatalf("guard consulted %d times for one node, want 1", guard.calls["dead_node_sweep"])
	}

	guard.allow = true
	reconciled, err = e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("ReconcileDeadNodeInstances: %v", err)
	}
	if reconciled != 2 {
		t.Fatalf("reconciled %d once the guard allowed it, want 2", reconciled)
	}
}
