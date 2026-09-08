// engine_emit_test.go — issue #517 / PR-C / ADR-064 — schedd is the
// canonical emit site for wake.queue_accepted, wake.admitted,
// wake.boot_started, wake.boot_completed, wake.boot_failed,
// wake.park_started, wake.park_completed, and wake.stalled. These
// tests pin the engine's emit sequence by reading the events
// table back via state.Store.ListEventsByWakeID (the same query
// the customer-facing timeline endpoint uses). The recording path
// is the production emit path: events.NewPlatform writes to the
// store; the test queries back. This mirrors the upstream
// read-side contract end-to-end.
package sched

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// wakeEngineWithEvents builds an Engine with the events.Platform
// wired. The Platform writes to the supplied store (MemStore) so
// the test can read the events rows back via
// store.ListEventsByWakeID. Returns the engine for the assertion.
func wakeEngineWithEvents(t *testing.T, store state.Store, vmm RoutedVMM, notif Notifier) *Engine {
	t.Helper()
	e, err := NewEngine(context.Background(), store, NewNodeLedger(), vmm, notif, "1.10.0", testLog())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	e.WithEvents(events.NewPlatform("schedd", store, testLog(), nil, nil))
	return e
}

// eventsForInstance collects the events rows the engine wrote
// for the given wake_id. The returned slice is in ASC order
// (oldest → newest), matching the production ListEventsByWakeID
// contract.
func eventsForInstance(t *testing.T, store state.Store, wakeID string) []state.Event {
	t.Helper()
	if wakeID == "" {
		return nil
	}
	rows, err := store.ListEventsByWakeID(context.Background(), wakeID, time.Time{}, 0)
	if err != nil {
		t.Fatalf("ListEventsByWakeID: %v", err)
	}
	return rows
}

func eventuallyEventsForInstance(t *testing.T, store state.Store, wakeID string, want int) []state.Event {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		rows := eventsForInstance(t, store, wakeID)
		if len(rows) >= want || time.Now().After(deadline) {
			return rows
		}
		time.Sleep(time.Millisecond)
	}
}

// kindsOf returns the `.Kind` slice of an events row set.
func kindsOf(rows []state.Event) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Kind
	}
	return out
}

// contains reports whether `kinds` has `want`. The kind list is
// small (≤ 6 entries per wake); a linear scan is the cleanest
// shape.
func contains(kinds []string, want string) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// TestEngineWake_EmitsCanonicalSequence pins the wake.* event
// surface: a cold boot on a fresh app must emit queue_accepted,
// admitted, boot_started, then boot_completed in that order. The
// pair-key (kind, wake_id) is the join path the customer-facing
// timeline endpoint takes.
func TestEngineWake_EmitsCanonicalSequence(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := wakeEngineWithEvents(t, store, vmm, notif)
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	want := []string{
		"wake.queue_accepted",
		"wake.admitted",
		"wake.boot_started",
		"wake.boot_completed",
	}
	rows := eventuallyEventsForInstance(t, store, res.WakeID, len(want))
	if len(rows) != len(want) {
		t.Fatalf("rows = %v, want %d (kinds=%v)", kindsOf(rows), len(want), want)
	}
	for i, r := range rows {
		if r.Kind != want[i] {
			t.Errorf("rows[%d].Kind = %q, want %q", i, r.Kind, want[i])
		}
	}
}

// TestEngineKillStuck_EmitsStalled pins the watchdog timeout
// emit. wake.stalled is the typed counterpart of the legacy
// watchdog_timeout audit row; the plan keeps both for backwards
// compat.
func TestEngineKillStuck_EmitsStalled(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := wakeEngineWithEvents(t, store, vmm, notif)
	// Wake to seed an instance, then roll it back to WAKING so
	// KillStuck recognizes it as stuck.
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	ins, err := store.RunningInstanceForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if err := store.UpdateInstanceStateWithTimestamp(context.Background(), ins.ID, string(state.StateWaking), time.Now().Add(-10*time.Minute)); err != nil {
		t.Fatalf("seed WAKING: %v", err)
	}
	if err := e.KillStuck(context.Background(), ins.ID, app.ID, StuckWakingTimeout); err != nil {
		t.Fatalf("KillStuck: %v", err)
	}
	rows := eventsForInstance(t, store, ins.WakeID)
	if !contains(kindsOf(rows), "wake.stalled") {
		t.Errorf("kinds missing wake.stalled: got %v", kindsOf(rows))
	}
}

// TestEnginePark_EmitsStartedCompleted pins the park-timeline
// pair. A successful park must emit wake.park_started at the
// RUNNING→SNAPSHOTTING transition and wake.park_completed at the
// SNAPSHOTTING→PARKED transition.
func TestEnginePark_EmitsStartedCompleted(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := wakeEngineWithEvents(t, store, vmm, notif)
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	ins, err := store.RunningInstanceForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if err := e.Park(context.Background(), ins.ID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	rows := eventsForInstance(t, store, ins.WakeID)
	got := kindsOf(rows)
	if !contains(got, "wake.park_started") {
		t.Errorf("kinds missing wake.park_started: got %v", got)
	}
	if !contains(got, "wake.park_completed") {
		t.Errorf("kinds missing wake.park_completed: got %v", got)
	}
}

// TestEnginePark_TailDrainWatchdogSucceedsWhenCounterDrains pins
// the snapshotAndPark watchdog (issue #667 / ADR-078 §"Reaper gate"
// / §"Park gate"): when an instance has TailCount > 0 at Park
// entry, the watchdog polls for ParkTailDrainTimeoutSeconds and
// force-parks only if the counter does not drop to 0. This test
// exercises the success path: the runner's tail host drains all
// tasks mid-poll, the watchdog sees 0, and the park proceeds
// without emitting any wake.tail_failed events.
//
// The deadline path (5s of unfinished tails → force-park +
// wake.tail_failed{reason=forced_at_park}) is exercised by
// pkg/fcvm/tail_metal_test.go::TestMetal_TailEndToEnd; the unit
// test here pins the fast-path because spinning the deadline
// would cost 5s per call. The load-bearing invariant under test
// is "the watchdog does NOT force-park when the runner drains
// in time" — a regression here would break every customer wake
// that uses waitUntil.
func TestEnginePark_TailDrainWatchdogSucceedsWhenCounterDrains(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := wakeEngineWithEvents(t, store, vmm, notif)
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}

	// Bump tail_count to 3 (simulating three in-flight waitUntil
	// tasks). The watchdog in snapshotAndPark must wait until
	// GetInstanceTailCount returns 0 before proceeding.
	const initialTails = int32(3)
	if _, err := store.BumpInstanceTailCount(context.Background(), res.InstanceID, initialTails); err != nil {
		t.Fatalf("seed BumpInstanceTailCount: %v", err)
	}
	post, err := store.GetInstanceTailCount(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("GetInstanceTailCount: %v", err)
	}
	if post != initialTails {
		t.Fatalf("post-bump tail_count = %d, want %d", post, initialTails)
	}

	// Simulate the runner's tail host draining all three tasks
	// mid-poll. Use a goroutine that fires AFTER the Park call
	// has entered the watchdog loop; the 200ms poll interval in
	// snapshotAndPark gives the drain a generous window.
	go func() {
		// 50ms is enough to land mid-poll but well under the
		// 5s deadline — the test fails fast if the watchdog is
		// broken (no waiting for the deadline).
		time.Sleep(50 * time.Millisecond)
		_ = store.DecrementInstanceTailCount(context.Background(), res.InstanceID, initialTails)
	}()

	if err := e.Park(context.Background(), res.InstanceID); err != nil {
		t.Fatalf("Park: %v", err)
	}

	// Assertion 1: the post-Park tail_count is 0 (the watchdog
	// saw the drain and the racy SQL decrement by the dummy
	// goroutine did not bump the post-park state). MemStore's
	// AppendUsage / DecrementInstanceTailCount is idempotent.
	post, err = store.GetInstanceTailCount(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("post-Park GetInstanceTailCount: %v", err)
	}
	if post != 0 {
		t.Errorf("post-Park tail_count = %d, want 0 (drain + decrement should leave 0)", post)
	}

	// Assertion 2: NO wake.tail_failed events were emitted
	// (the runner drained successfully). The watchdog's
	// forced_at_park audit row is the load-bearing indicator
	// of a stuck drain — its absence confirms the success
	// path.
	rows := eventsForInstance(t, store, res.WakeID)
	got := kindsOf(rows)
	if contains(got, "wake.tail_failed") {
		t.Errorf("kinds contained wake.tail_failed but the runner drained in time; got %v", got)
	}

	// Assertion 3: the instance terminated cleanly (park
	// succeeded, state is STOPPED). Without the watchdog's
	// success path, the park would either hang for 5s and
	// force-park (emitting wake.tail_failed) or fail with a
	// hung-snapshot error.
	ins, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if ins.State != string(state.StateParked) {
		t.Errorf("post-Park state = %q, want parked", ins.State)
	}
}
