// adr: 1344
// store_settle_test.go pins ADR-1344 §5 rule 4: after this schedd saw the
// store unavailable, liveness verdicts that compare durable heartbeat
// timestamps with "now" wait until it has read the store without
// interruption for a full staleness window, so a store outage cannot mark
// the whole fleet dead. A schedd that saw no outage gives verdicts at once.

package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// adr: 1344
func TestStoreSettle(t *testing.T) {
	var s storeSettle
	t0 := time.Now()
	window := time.Minute
	s.observe(nil, t0)
	if ok, first := s.settled(t0, window); !ok || first {
		t.Fatalf("no outage seen: settled=%v first=%v, want true/false", ok, first)
	}
	s.observe(errors.New("outage"), t0.Add(window))
	if ok, first := s.settled(t0.Add(window), window); ok || !first {
		t.Fatalf("after error settled=%v first=%v, want false/true", ok, first)
	}
	s.observe(nil, t0.Add(2*window))
	if ok, first := s.settled(t0.Add(2*window+30*time.Second), window); ok || first {
		t.Fatalf("mid-window settled=%v first=%v, want false/false", ok, first)
	}
	// A later success does not move the start of the settle period.
	s.observe(nil, t0.Add(2*window+30*time.Second))
	if ok, _ := s.settled(t0.Add(3*window), window); !ok {
		t.Fatal("not settled one window after recovery")
	}
	// An outage seen before the tracker existed holds it the same way.
	s.hold()
	if ok, first := s.settled(t0.Add(4*window), window); ok || !first {
		t.Fatalf("after hold settled=%v first=%v, want false/true", ok, first)
	}
	s.observe(nil, t0.Add(4*window))
	if ok, _ := s.settled(t0.Add(5*window), window); !ok {
		t.Fatal("not settled one window after a held start")
	}
}

// backdateSettle moves the start of the current settle period to t, as if
// store reads had succeeded since then.
func backdateSettle(s *storeSettle, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.since = t
}

// deadNodeListFailStore fails ListRunningInstancesOnDeadNodes while fail is
// set, standing in for a Postgres outage seen by the dead-node sweep.
type deadNodeListFailStore struct {
	MemStore *state.MemStore
	fail     bool
}

func (s *deadNodeListFailStore) ListRunningInstancesOnDeadNodes(ctx context.Context, threshold time.Time, limit int) ([]state.Instance, error) {
	if s.fail {
		return nil, errors.New("postgres unavailable")
	}
	return s.MemStore.ListRunningInstancesOnDeadNodes(ctx, threshold, limit)
}

// adr: 1344
// TestReconcileDeadNodeInstances_WaitsForStoreToSettle: right after a store
// outage the sweep leaves stale-heartbeat rows alone; once the schedd has
// read the store for the staleness window and the node is still silent, the
// row fails as before.
func TestReconcileDeadNodeInstances_WaitsForStoreToSettle(t *testing.T) {
	mem := state.NewMemStore()
	store := &deadNodeListFailStore{MemStore: mem}
	e := newEngine(t, statetest.NewMem(store), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	nodeID := seedStaleComputeNodeForReconcile(t, mem)
	_, insID := seedRunningInstance(t, mem, nodeID)

	store.fail = true
	if _, err := e.ReconcileDeadNodeInstances(context.Background()); err == nil {
		t.Fatal("list failure was not reported")
	}
	store.fail = false
	reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("first tick after outage: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconciled=%d right after a store outage, want 0", reconciled)
	}
	ins, err := mem.InstanceByID(context.Background(), insID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if ins.State != string(state.StateRunning) {
		t.Fatalf("instance state right after outage = %q, want running", ins.State)
	}

	backdateSettle(&e.deadNodeSettle, time.Now().Add(-3*time.Minute))
	reconciled, err = e.ReconcileDeadNodeInstances(context.Background())
	if err != nil {
		t.Fatalf("settled tick: %v", err)
	}
	if reconciled != 1 {
		t.Fatalf("reconciled=%d after settling, want 1", reconciled)
	}
}

// adr: 1344
// TestReconcileDeadNodeInstances_StartupHoldNeedsAnOutage: a schedd that
// started against a healthy store fails a dead node's instances on its first
// sweep. One that saw the store unavailable before it started (a boot wait
// or store errors as a standby) holds them for the staleness window.
func TestReconcileDeadNodeInstances_StartupHoldNeedsAnOutage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		outage bool
		want   int
	}{
		{"healthy start", false, 1},
		{"outage before start", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := state.NewMemStore()
			e := newEngine(t, mem, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithStoreOutageAtStartup(tc.outage)
			nodeID := seedStaleComputeNodeForReconcile(t, mem)
			seedRunningInstance(t, mem, nodeID)
			reconciled, err := e.ReconcileDeadNodeInstances(context.Background())
			if err != nil {
				t.Fatalf("first sweep: %v", err)
			}
			if reconciled != tc.want {
				t.Fatalf("first sweep reconciled=%d, want %d", reconciled, tc.want)
			}
		})
	}
}

// nodeListFailStore fails NodeList while fail is set, standing in for a
// Postgres outage seen by the stale-peer observer.
type nodeListFailStore struct {
	MemStore *state.MemStore
	fail     bool
}

func (s *nodeListFailStore) NodeList(ctx context.Context, lifecycle state.NodeLifecycle) ([]state.ComputeNode, error) {
	if s.fail {
		return nil, errors.New("postgres unavailable")
	}
	return s.MemStore.NodeList(ctx, lifecycle)
}

// adr: 1344
// TestHeartbeat_StalePeerObserverWaitsForStoreToSettle: right after a store
// outage every peer's heartbeat is old because nobody could write it, so the
// observer must not mark peers unavailable until it has read the store for a
// full staleness window.
func TestHeartbeat_StalePeerObserverWaitsForStoreToSettle(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	store := &nodeListFailStore{MemStore: mem}
	owner, err := mem.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName owner: %v", err)
	}
	now := time.Now().UTC()
	peer, err := mem.CreateComputeNode(ctx, state.ComputeNode{
		Name:            "peer-after-outage",
		TargetURL:       "tcp://10.0.0.23:50051",
		Lifecycle:       state.NodeLifecycleActive,
		Active:          true,
		LastHeartbeatAt: now.Add(-10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateComputeNode peer: %v", err)
	}
	h := NewHeartbeat(statetest.NewMem(store), &heartbeatFakeDialer{}, nil, nil).
		WithOwnerNodeID(owner.ID).
		WithStalePeerObserver(true)
	h.Staleness = time.Minute
	clock := now
	h.now = func() time.Time { return clock }
	lifecycle := func() state.NodeLifecycle {
		t.Helper()
		n, err := mem.ComputeNodeByID(ctx, peer.ID)
		if err != nil {
			t.Fatalf("ComputeNodeByID peer: %v", err)
		}
		return n.Lifecycle
	}

	store.fail = true
	_ = h.Tick(ctx)
	store.fail = false
	_ = h.Tick(ctx)
	if got := lifecycle(); got != state.NodeLifecycleActive {
		t.Fatalf("peer lifecycle right after store outage = %q, want active", got)
	}

	clock = now.Add(h.Staleness)
	_ = h.Tick(ctx)
	if got := lifecycle(); got != state.NodeLifecycleUnavailable {
		t.Fatalf("peer lifecycle after settling = %q, want unavailable", got)
	}
}
