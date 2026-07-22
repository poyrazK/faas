// retention_test.go (PR #74, spec §17 follow-up). The retention
// sweep lives in pkg/sched and runs as a 4th ticker in Loop.Run; this
// file pins its behaviour at the unit level.
//
// Test shape mirrors watchdog_test.go and cron_loop_test.go: build a
// MemStore + Engine, drive loop.runRetention directly so the test
// doesn't need a real ticker.
package sched

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// retentionMemStore unwraps a state.Store to *state.MemStore so the
// SetTerminalAtForTest helper can fabricate terminal_at timestamps
// older than the retention window. Mirrors watchdog_test.go's
// toMemStore shape.
func retentionMemStore(t *testing.T, s state.Store) *state.MemStore {
	t.Helper()
	m, ok := s.(*state.MemStore)
	if !ok {
		t.Skipf("retention tests require *state.MemStore; got %T", s)
	}
	return m
}

// TestRetentionSweepsTerminalRows pins the happy-path sweep: rows in
// {STOPPED, FAILED} older than the retention window get DELETED;
// younger rows + every non-terminal row are left alone.
func TestRetentionSweepsTerminalRows(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	ms := retentionMemStore(t, store)

	// One row per state × two ages = 10 fixtures.
	states := []string{
		string(state.StateStopped),
		string(state.StateFailed),
		string(state.StateParked),
		string(state.StateRunning),
		string(state.StateColdBooting),
	}
	ids := make(map[string]string, len(states)*2)
	for _, st := range states {
		oldRow, err := store.CreateInstance(context.Background(), app.ID, dep.ID, st, 512, state.DefaultLocalNodeName)
		if err != nil {
			t.Fatalf("CreateInstance(old, %s): %v", st, err)
		}
		ids["old-"+st] = oldRow.ID
		// Only terminal rows need a terminal_at for the sweep to see
		// them. Other states get a StartedAt backdate so we also
		// confirm the sweep NEVER touches them by mistake.
		if st == string(state.StateStopped) || st == string(state.StateFailed) {
			ms.SetTerminalAtForTest(oldRow.ID, time.Now().Add(-40*24*time.Hour))
		} else {
			ms.BackdateForTest(oldRow.ID, time.Now().Add(-40*24*time.Hour))
		}

		youngRow, err := store.CreateInstance(context.Background(), app.ID, dep.ID, st, 512, state.DefaultLocalNodeName)
		if err != nil {
			t.Fatalf("CreateInstance(young, %s): %v", st, err)
		}
		ids["young-"+st] = youngRow.ID
		if st == string(state.StateStopped) || st == string(state.StateFailed) {
			ms.SetTerminalAtForTest(youngRow.ID, time.Now().Add(-5*24*time.Hour))
		}
	}

	r := NewRetention(store, slog.Default()).
		WithRetention(30 * 24 * time.Hour).
		WithClock(func() time.Time { return time.Now() })

	deleted, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (one old STOPPED + one old FAILED)", deleted)
	}

	// Old terminal rows must be gone.
	for _, st := range []string{string(state.StateStopped), string(state.StateFailed)} {
		if _, err := store.InstanceByID(context.Background(), ids["old-"+st]); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("old %s row still present: %v", st, err)
		}
	}
	// Young terminal rows + every non-terminal row must remain.
	for _, key := range []string{
		"young-" + string(state.StateStopped),
		"young-" + string(state.StateFailed),
		"old-" + string(state.StateParked),
		"old-" + string(state.StateRunning),
		"old-" + string(state.StateColdBooting),
		"young-" + string(state.StateParked),
		"young-" + string(state.StateRunning),
		"young-" + string(state.StateColdBooting),
	} {
		if _, err := store.InstanceByID(context.Background(), ids[key]); err != nil {
			t.Errorf("%s row went missing: %v", key, err)
		}
	}
}

// TestRetentionDoubleTickIsIdempotent pins the redelivery contract:
// a second sweep on the same DB finds zero rows and is a no-op.
func TestRetentionDoubleTickIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	ms := retentionMemStore(t, store)
	old, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateStopped), 512, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	ms.SetTerminalAtForTest(old.ID, time.Now().Add(-40*24*time.Hour))

	r := NewRetention(store, slog.Default()).WithRetention(30 * 24 * time.Hour)
	first, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce #1: %v", err)
	}
	if first != 1 {
		t.Errorf("first sweep deleted %d, want 1", first)
	}
	second, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce #2: %v", err)
	}
	if second != 0 {
		t.Errorf("second sweep deleted %d, want 0 (idempotent)", second)
	}
}

// TestRetentionSkipActiveStates is a focused negative test: a STOPPED
// row with terminal_at = NULL is invisible to the sweep (the SQL
// predicate `terminal_at is not null` would skip it; MemStore mirrors).
// Belt-and-braces against a future regression that swaps the column
// for started_at.
func TestRetentionSkipActiveStates(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)

	// Create a STOPPED row WITHOUT a terminal_at (engine bug or
	// pre-migration backfill regression). Must not be swept.
	row, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateStopped), 512, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	r := NewRetention(store, slog.Default()).WithRetention(30 * 24 * time.Hour)
	deleted, err := r.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0 (row has no terminal_at)", deleted)
	}
	if _, err := store.InstanceByID(context.Background(), row.ID); err != nil {
		t.Errorf("NULL-terminal_at row went missing: %v", err)
	}
}

// TestLoopRunRetentionWiresThroughLoop pins the Loop integration:
// WithRetention attaches a sweep; runRetention drives one tick; the
// sweep sees the same rows as a direct SweepOnce call.
func TestLoopRunRetentionWiresThroughLoop(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	ms := retentionMemStore(t, store)

	old, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateFailed), 512, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	ms.SetTerminalAtForTest(old.ID, time.Now().Add(-40*24*time.Hour))

	r := NewRetention(store, slog.Default()).WithRetention(30 * 24 * time.Hour)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, slog.Default()).WithRetention(r)

	loop.runRetention(context.Background())

	if _, err := store.InstanceByID(context.Background(), old.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("old FAILED row still present after loop.runRetention: %v", err)
	}
}

// TestTransitionStampsTerminalAt pins the Engine.transition side of
// the contract: a successful transition to STOPPED (or FAILED) writes
// terminal_at on the row in the same UPDATE. This is the producer
// the retention sweep consumes from.
func TestTransitionStampsTerminalAt(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOpsMetrics(wire.NewOpsMetrics("schedd"))

	res, err := engine.Wake(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// Drive the instance to STOPPED via Park with a failing snapshot.
	vmm.snapErr = errBoom
	if err := engine.Park(context.Background(), res.InstanceID); err == nil {
		t.Fatal("expected Park to fail (snapErr set)")
	}
	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateStopped) {
		t.Errorf("state = %q, want STOPPED", row.State)
	}
	if row.TerminalAt == nil {
		t.Fatal("terminal_at = nil after STOPPED transition (PR #74 contract)")
	}
	if time.Since(*row.TerminalAt) > 10*time.Second {
		t.Errorf("terminal_at = %v, want ~now()", *row.TerminalAt)
	}
}
