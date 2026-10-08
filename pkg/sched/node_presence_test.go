// adr: 419 — only fresh authoritative inventory may repair a missing service VM.
package sched

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodePresenceTrackerRequiresDistinctFreshReports(t *testing.T) {
	now := time.Now().UTC()
	tracker := newNodePresenceTracker()
	inventory := NodeInstanceInventory{NodeID: "node-a", Complete: true, SampledAt: now}
	if _, ok := tracker.Observe(inventory, now); ok {
		t.Fatal("first empty report confirmed absence")
	}
	if _, ok := tracker.Observe(inventory, now); ok {
		t.Fatal("replayed report confirmed absence")
	}
	inventory.SampledAt = now.Add(time.Second)
	observation, ok := tracker.Observe(inventory, inventory.SampledAt)
	if !ok {
		t.Fatal("second distinct empty report did not confirm absence")
	}
	tracker.markReconciled(inventory.NodeID, observation.fingerprint)
	inventory.SampledAt = now.Add(2 * time.Second)
	if _, ok := tracker.Observe(inventory, inventory.SampledAt); ok {
		t.Fatal("reconciled report checked on every tick")
	}
}

func TestNodePresenceTrackerPreservesInstanceIDBoundaries(t *testing.T) {
	tracker := newNodePresenceTracker()
	now := time.Now().UTC()
	tracker.Observe(NodeInstanceInventory{NodeID: "n", Complete: true, InstanceIDs: []string{"a,b", "c"}, SampledAt: now}, now)
	next := now.Add(time.Second)
	if _, ok := tracker.Observe(NodeInstanceInventory{NodeID: "n", Complete: true, InstanceIDs: []string{"a", "b,c"}, SampledAt: next}, next); ok {
		t.Fatal("different process inventories counted as consecutive identical observations")
	}
}

func TestNodePresenceTrackerUnknownStaleAndInterruptedReportsResetConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		complete                    bool
		ids                         []string
		sampleOffset, receiveOffset time.Duration
	}{
		{name: "unknown"},
		{name: "stale", complete: true, sampleOffset: -10 * time.Second},
		{name: "future", complete: true, sampleOffset: 10 * time.Second},
		{name: "empty-id", complete: true, ids: []string{""}},
		{name: "duplicate-id", complete: true, ids: []string{"vm-a", "vm-a"}},
		{name: "interruption", complete: true, sampleOffset: 10 * time.Second, receiveOffset: 10 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newNodePresenceTracker()
			now := time.Now().UTC()
			tracker.Observe(NodeInstanceInventory{NodeID: "n", Complete: true, SampledAt: now}, now)
			_, ok := tracker.Observe(NodeInstanceInventory{NodeID: "n", Complete: tc.complete, InstanceIDs: tc.ids, SampledAt: now.Add(tc.sampleOffset)}, now.Add(tc.receiveOffset))
			if ok {
				t.Fatal("bad/interrupted inventory triggered reconciliation")
			}
			fresh := now.Add(tc.receiveOffset + time.Second)
			if _, ok := tracker.Observe(NodeInstanceInventory{NodeID: "n", Complete: true, SampledAt: fresh}, fresh); ok && tc.name != "interruption" {
				t.Fatal("first report after invalid inventory confirmed absence")
			}
		})
	}
}

type inventoryFixture struct {
	store    *state.MemStore
	engine   *Engine
	vmm      *fakeVMM
	notifier *fakeNotifier
	now      time.Time
	clock    atomic.Int64
	instance state.Instance
	work     *workPool
}

func newInventoryFixture(t *testing.T, service bool) *inventoryFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	vmm := &fakeVMM{}
	notifier := &fakeNotifier{}
	engine := newEngine(t, store, vmm, notifier, "1.10.0")
	// Install the atomic clock before any VM work so asynchronous reconcile
	// callbacks never observe engine.now being replaced.
	f := &inventoryFixture{store: store, engine: engine, vmm: vmm, notifier: notifier, now: time.Now().UTC()}
	f.clock.Store(f.now.UnixNano())
	engine.now = func() time.Time { return time.Unix(0, f.clock.Load()) }
	var ins state.Instance
	if service {
		// Use the production submission path and finish bootstrap notifications
		// before advancing fake time; no recovery callback may escape the fixture.
		loop := NewLoop(nil, engine, testLog()).WithClock(engine.now)
		f.work = loop.workPool()
		t.Cleanup(f.work.drain)
		manifest := state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 1, Desired: 1}}
		if _, err := store.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
			t.Fatal(err)
		}
		engine.convergeServiceReplicas(ctx, dep.ID)
		loop.workPool().drain()
		rows, err := store.ListInstancesForApp(ctx, app.ID)
		if err != nil || len(rows) != 1 {
			t.Fatalf("seed replicas=%v err=%v", rows, err)
		}
		ins = rows[0]
	} else {
		node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
		if err != nil {
			t.Fatal(err)
		}
		ins, err = store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.Ledger().Admit(Request{AppID: app.ID, Instance: ins.ID, NodeID: node.ID, RAMMB: 128, Plan: api.PlanPro, MaxConcurrency: 5}); err != nil {
			t.Fatal(err)
		}
	}
	f.instance = ins
	f.now = f.now.Add(90 * time.Second)
	f.clock.Store(f.now.UnixNano())
	return f
}

func (f *inventoryFixture) report(complete bool, ids ...string) {
	f.clock.Store(f.now.UnixNano())
	f.engine.ObserveNodeInventory(context.Background(), NodeInstanceInventory{NodeID: f.instance.NodeID, Complete: complete, InstanceIDs: ids, SampledAt: f.now})
	f.now = f.now.Add(time.Second)
}

func (f *inventoryFixture) assertState(t *testing.T, want state.State) state.Instance {
	t.Helper()
	row, err := f.store.InstanceByID(context.Background(), f.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.State != string(want) {
		t.Fatalf("instance state=%s, want %s", row.State, want)
	}
	return row
}

func TestNodeInventoryRepairsOnlyMissingRunningInstances(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	for _, st := range []state.State{state.StateRunning, state.StateWaking, state.StateColdBooting, state.StateMigrating, state.StateSnapshotting, state.StateDraining} {
		t.Run(string(st), func(t *testing.T) {
			f := newInventoryFixture(t, false)
			if err := f.store.UpdateInstanceState(context.Background(), f.instance.ID, string(st)); err != nil {
				t.Fatal(err)
			}
			f.report(true)
			f.report(true)
			if st == state.StateRunning {
				row := f.assertState(t, state.StateFailed)
				if row.TerminalAt == nil {
					t.Fatal("terminal timestamp absent")
				}
				if f.engine.Ledger().ResidentFor(row.ID) {
					t.Fatal("dead VM still reserves capacity")
				}
				if f.vmm.destroys != 1 {
					t.Fatalf("cleanup calls=%d, want 1", f.vmm.destroys)
				}
			} else {
				f.assertState(t, st)
				if f.vmm.destroys != 0 {
					t.Fatal("destroyed a transitioning VM")
				}
			}
		})
	}
}

func TestNodeInventoryProtectsPresentAndFreshInstances(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	t.Run("present", func(t *testing.T) {
		f := newInventoryFixture(t, false)
		f.report(true, f.instance.ID)
		f.report(true, f.instance.ID)
		f.assertState(t, state.StateRunning)
	})
	t.Run("startup grace", func(t *testing.T) {
		f := newInventoryFixture(t, false)
		f.now = f.instance.StartedAt.Add(time.Second)
		f.report(true)
		f.report(true)
		f.assertState(t, state.StateRunning)
		// Continuous reports eventually repair after grace, without waiting for
		// the steady-inventory refresh interval or a customer request.
		for i := 0; i < api.InstanceDivergenceGraceSeconds+2; i++ {
			f.report(true)
		}
		f.assertState(t, state.StateFailed)
	})
}

func TestNodeInventoryReportOnlyDoesNotDestroyOrMutate(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "")
	f := newInventoryFixture(t, false)
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	if f.vmm.destroys != 0 || !f.engine.Ledger().ResidentFor(f.instance.ID) {
		t.Fatal("report-only changed host capacity")
	}
}

func TestNodeInventoryCleanupFailureRetriesBeforeReplacement(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	f.vmm.destroyErr = errors.New("temporarily unavailable")
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	if !f.engine.Ledger().ResidentFor(f.instance.ID) {
		t.Fatal("released capacity before cleanup")
	}
	f.vmm.destroyErr = nil
	f.report(true)
	f.assertState(t, state.StateFailed)
}

func TestNodeInventoryServiceRecoversWithoutTrafficAndNotifiesRouting(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	f.report(true)
	dead := f.assertState(t, state.StateFailed)
	if f.engine.Ledger().ResidentFor(dead.ID) {
		t.Fatal("dead replica capacity leaked")
	}
	deadline := time.Now().Add(3 * time.Second)
	var replacement state.Instance
	for time.Now().Before(deadline) {
		rows, err := f.store.ListInstancesForApp(context.Background(), dead.AppID)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID != dead.ID && row.State == string(state.StateRunning) {
				replacement = row
			}
		}
		if replacement.ID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if replacement.ID == "" {
		t.Fatal("service was not replaced without a customer request")
	}
	for i := 0; i < 3; i++ {
		f.report(true, replacement.ID)
	}
	rows, _ := f.store.ListInstancesForApp(context.Background(), dead.AppID)
	live := 0
	for _, row := range rows {
		if state.IsLive(row.State) {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("live replicas=%d, want exactly 1", live)
	}
	// The worker commits RUNNING before publishing its route invalidation.
	// Join that worker within the original recovery deadline before inspecting
	// notifications; observing the row alone does not prove it has returned.
	finished := make(chan struct{})
	go func() {
		f.work.drain()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Until(deadline)):
		t.Fatal("service recovery worker did not finish route invalidation notifications")
	}
	f.notifier.mu.Lock()
	defer f.notifier.mu.Unlock()
	failedNotified, replacementNotified := false, false
	for _, event := range f.notifier.events {
		if event.channel != db.NotifyInstanceChanged {
			continue
		}
		var payload struct {
			InstanceID string `json:"instance_id"`
			State      string `json:"state"`
		}
		if err := json.Unmarshal([]byte(event.payload), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.InstanceID == dead.ID && payload.State == string(state.StateFailed) {
			failedNotified = true
		}
		if payload.InstanceID == replacement.ID && payload.State == string(state.StateRunning) {
			replacementNotified = true
		}
	}
	if !failedNotified || !replacementNotified {
		t.Fatal("missing dead/replacement route invalidation notifications")
	}
}

func TestNodeInventoryRestartAndTelemetryLossDoNotCombineOldEvidence(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	f.report(true)
	f.engine.nodePresence = newNodePresenceTracker()
	f.report(true)
	f.assertState(t, state.StateRunning)
	f.report(false)
	f.report(true)
	f.assertState(t, state.StateRunning)
	f.report(true)
	f.assertState(t, state.StateFailed)
}

// A physical replica can live on a node different from its app's scheduler owner.
func TestNodeInventoryUsesPhysicalPlacement(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	owner := "different-scheduler-owner"
	if err := f.store.SetAppNodeID(context.Background(), f.instance.AppID, owner); err != nil {
		t.Fatal(err)
	}
	f.engine.WithOwnerNodeID(owner)
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateFailed)
}

func TestNodeInventoryDoesNotRepairAnotherSchedulersApp(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	if err := f.store.SetAppNodeID(context.Background(), f.instance.AppID, "another-owner"); err != nil {
		t.Fatal(err)
	}
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	if f.vmm.destroys != 0 || !f.engine.Ledger().ResidentFor(f.instance.ID) {
		t.Fatal("changed host capacity for another scheduler's app")
	}
}

type failingInventoryStore struct {
	*state.MemStore
	fail bool
}

func (s *failingInventoryStore) FailRunningInstanceIfOwnedByNode(ctx context.Context, id, nodeID string, terminalAt time.Time) error {
	if s.fail {
		return errors.New("temporary database failure")
	}
	return s.MemStore.FailRunningInstanceIfOwnedByNode(ctx, id, nodeID, terminalAt)
}

func TestNodeInventoryDatabaseFailureRetriesWithoutReleasingCapacity(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	store := &failingInventoryStore{MemStore: f.store, fail: true}
	f.engine.store = store
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	if !f.engine.Ledger().ResidentFor(f.instance.ID) {
		t.Fatal("released reservation after failed terminal write")
	}
	store.fail = false
	f.report(true)
	f.assertState(t, state.StateFailed)
}

func TestNodeInventoryUnavailableCleanupDoesNotReleaseCapacity(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	f.engine.vmm = nil
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	if !f.engine.Ledger().ResidentFor(f.instance.ID) {
		t.Fatal("released reservation without a cleanup owner")
	}
	f.engine.vmm = f.vmm
	f.report(true)
	f.assertState(t, state.StateFailed)
}

type restartingInventoryStore struct {
	*state.MemStore
	startedAt time.Time
}

func (s *restartingInventoryStore) InstanceByID(ctx context.Context, id string) (state.Instance, error) {
	row, err := s.MemStore.InstanceByID(ctx, id)
	row.StartedAt = s.startedAt
	return row, err
}

func TestNodeInventoryRereadProtectsRestartedInstance(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	f.engine.store = &restartingInventoryStore{MemStore: f.store, startedAt: f.now.Add(-time.Second)}
	f.report(true)
	f.report(true)
	f.assertState(t, state.StateRunning)
	if f.vmm.destroys != 0 {
		t.Fatal("destroyed an instance whose latest start is within startup grace")
	}
}

func TestNodeInventorySupersededObservationCannotDestroyReappearedVM(t *testing.T) {
	t.Setenv(NodeInventoryEnforceEnv, "1")
	f := newInventoryFixture(t, false)
	// A newer complete inventory changes the fingerprint while an older
	// observation is waiting for its app lifecycle lock.
	f.engine.nodePresence.Observe(NodeInstanceInventory{NodeID: f.instance.NodeID, Complete: true, SampledAt: f.now}, f.now)
	observation, ok := f.engine.nodePresence.Observe(NodeInstanceInventory{NodeID: f.instance.NodeID, Complete: true, SampledAt: f.now.Add(time.Second)}, f.now.Add(time.Second))
	if !ok {
		t.Fatal("fixture did not confirm absence")
	}
	f.engine.nodePresence.Observe(NodeInstanceInventory{NodeID: f.instance.NodeID, Complete: true, InstanceIDs: []string{f.instance.ID}, SampledAt: f.now.Add(2 * time.Second)}, f.now.Add(2*time.Second))
	if f.engine.nodePresence.current(f.instance.NodeID, observation.fingerprint, f.now.Add(2*time.Second)) {
		t.Fatal("superseded empty report remains eligible for cleanup")
	}
}
