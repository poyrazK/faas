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
