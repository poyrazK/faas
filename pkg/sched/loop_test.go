package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestLoopReaperParksIdleInstance drives one reaper tick against a store holding
// an instance well past its idle timeout and asserts the engine parked it
// (snapshot taken, RAM released). The Loop reads instances from the store, so we
// back-date last_request_at by transitioning through a real wake first.
func TestLoopReaperParksIdleInstance(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// Make the instance look long-idle: last_request_at far in the past.
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	loop := NewLoop(nil, engine, testLog())
	loop.runReaper(context.Background())

	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateParked) {
		t.Errorf("state = %q, want parked", ins.State)
	}
	if vmm.snapshots != 1 {
		t.Errorf("snapshots = %d, want 1 (idle park snapshots)", vmm.snapshots)
	}
	if got := engine.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 after park", got)
	}
}

// TestHandleSnapshotPrime routes a snapshot_prime notification into engine.Prime,
// producing a parked instance (the deploy-pipeline handoff, ADR-018).
func TestHandleSnapshotPrime(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	engine := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})

	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateParked) {
		t.Fatalf("rows = %+v, want one parked row", rows)
	}
}

// TestHandleNotificationRejectsBadInput covers the dispatch guards: malformed or
// incomplete payloads must not panic and must not act.
func TestHandleNotificationRejectsBadInput(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime, Payload: "{not json",
	})
	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime, Payload: `{"app_id":""}`,
	})
	loop.handleNotification(context.Background(), db.Notification{
		Channel: "no_such_channel", Payload: "{}",
	})
	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifyAppChanged, Payload: `{"app_id":"x"}`,
	})
}

// countingFlowCounter records every instance id passed to Open. Used by
// the snapshot-builder test to pin that runReaper consults the injected
// FlowCounter exactly once per instance (spec §17 G7).
type countingFlowCounter struct {
	calls map[string]int
	given map[string]int64
}

func newCountingFlowCounter(given map[string]int64) *countingFlowCounter {
	return &countingFlowCounter{calls: map[string]int{}, given: given}
}

func (c *countingFlowCounter) Open(_ context.Context, id string) (int64, error) {
	c.calls[id]++
	return c.given[id], nil
}

// TestRunReaperPopulatesOpenConns proves the snapshot builder asks the
// injected FlowCounter exactly once per instance and copies its
// result into InstanceInfo.OpenConns. Without this, the reaper's
// OpenConns skip rule would always see 0 — the production G7 fix
// would be permanently inert regardless of what the conntrack reader
// reports.
func TestRunReaperPopulatesOpenConns(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}

	// OpenConns > 0 + recent LastRequest ⇒ not reaped, but we want to
	// verify the snapshot saw the flow count. Push LastRequest far in the
	// past so without the flow count it WOULD be reaped; with it, isn't.
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	fc := newCountingFlowCounter(map[string]int64{res.InstanceID: 7})
	loop := NewLoop(nil, engine, testLog()).WithFlowCounter(fc)
	loop.runReaper(context.Background())

	if got := fc.calls[res.InstanceID]; got != 1 {
		t.Errorf("FlowCounter.Open calls = %d, want 1 (one per instance in snapshot)", got)
	}
	// LastRequest = -1h, plan default = 300s → would normally park. With
	// OpenConns > 0 (7) the G7 rule skips it.
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateRunning) {
		t.Errorf("state = %q, want running (G7: open flows kept it alive)", ins.State)
	}
}

// TestRunReaperFlowCounterErrorFailsOpen verifies the conservative
// fallback: a glitch in the flow source doesn't crash the reaper or
// permanently skip reaping. It logs and treats the count as 0, so the
// reaper uses only LastRequest for that instance.
func TestRunReaperFlowCounterErrorFailsOpen(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	bad := errorFlowCounter{err: assertErr{"conntrack timeout"}}
	loop := NewLoop(nil, engine, testLog()).WithFlowCounter(bad)
	loop.runReaper(context.Background())

	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateParked) {
		t.Errorf("state = %q, want parked (flow error fails open to LastRequest-only path)", ins.State)
	}
}

// assertErr is a sentinel error for the fails-open test. Defining it
// here avoids a polluting errors.New at package scope.
type assertErr struct{ s string }

func (e assertErr) Error() string { return e.s }

// errorFlowCounter always returns its configured error.
type errorFlowCounter struct{ err error }

func (e errorFlowCounter) Open(_ context.Context, _ string) (int64, error) { return 0, e.err }

// TestNoopFlowCounterIsTheDefault pins that a freshly-constructed
// Loop without WithFlowCounter uses noopFlowCounter — equivalent to
// "never skip for open connections", preserving prior behaviour for
// every existing test and for production until PR-B wires a real
// reader.
func TestNoopFlowCounterIsTheDefault(t *testing.T) {
	l := NewLoop(nil, nil, testLog())
	if l.flowCounts == nil {
		t.Fatal("loop.flowCounts is nil after NewLoop, want noopFlowCounter default")
	}
	got, err := l.flowCounts.Open(context.Background(), "any")
	if err != nil {
		t.Errorf("default FlowCounter.Open returned err = %v, want nil", err)
	}
	if got != 0 {
		t.Errorf("default FlowCounter.Open = %d, want 0", got)
	}
}
