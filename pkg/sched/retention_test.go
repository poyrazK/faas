// spec: §17
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
// {STOPPED, FAILED} older than the retention window and PARKED rows whose
// parked_at is old get DELETED; younger rows and resident/in-flight states
// are left alone.
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
		oldRow, err := store.CreateInstance(context.Background(), app.ID, dep.ID, st, 512, state.DefaultLocalNodeName, "")
		if err != nil {
			t.Fatalf("CreateInstance(old, %s): %v", st, err)
		}
		ids["old-"+st] = oldRow.ID
		// Only terminal rows need a terminal_at for the sweep to see
		// them. Other states get a StartedAt backdate so we also
		// confirm the sweep NEVER touches them by mistake.
		if st == string(state.StateStopped) || st == string(state.StateFailed) {
			ms.SetTerminalAtForTest(oldRow.ID, time.Now().Add(-40*24*time.Hour))
		} else if st == string(state.StateParked) {
			ms.SetParkedAtForTest(oldRow.ID, time.Now().Add(-40*24*time.Hour))
		} else {
			ms.BackdateForTest(oldRow.ID, time.Now().Add(-40*24*time.Hour))
		}

		youngRow, err := store.CreateInstance(context.Background(), app.ID, dep.ID, st, 512, state.DefaultLocalNodeName, "")
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
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3 (old STOPPED + FAILED + PARKED)", deleted)
	}

	// Old terminal and parked-history rows must be gone.
	for _, st := range []string{string(state.StateStopped), string(state.StateFailed), string(state.StateParked)} {
		if _, err := store.InstanceByID(context.Background(), ids["old-"+st]); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("old %s row still present: %v", st, err)
		}
	}
	// Young terminal rows + every non-terminal row must remain.
	for _, key := range []string{
		"young-" + string(state.StateStopped),
		"young-" + string(state.StateFailed),
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

// TestRetentionReclaimsRepeatedParkCyclesWithoutDeletingRestoreState models
// the production leak from #2415: every wake/park cycle leaves a new PARKED
// row, while the deployment's current snapshot is the reusable artifact.
// Old lifecycle history is reclaimed, but a recent parked row and every
// resident/in-flight state survive even when they carry an old parked_at from
// an earlier transition.
func TestRetentionReclaimsRepeatedParkCyclesWithoutDeletingRestoreState(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	ms := retentionMemStore(t, store)
	now := time.Date(2026, 9, 13, 18, 0, 0, 0, time.UTC)

	snap, err := store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID,
		FCVersion:    "1.10.0",
		MemBytes:     512 << 20,
		DiskBytes:    64 << 20,
		StorageKey:   state.SnapMemKey(dep.ID),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	// Supersession does not make the current/previous deployment snapshot
	// unusable, but its old parked instance rows must still age out.
	if err := store.MarkDeploymentSuperseded(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}

	var obsolete []string
	for cycle := 0; cycle < 8; cycle++ {
		ins, createErr := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
		if createErr != nil {
			t.Fatalf("CreateInstance(cycle %d): %v", cycle, createErr)
		}
		if parkErr := store.UpdateInstanceStateIf(ctx, ins.ID, string(state.StateRunning), string(state.StateParked)); parkErr != nil {
			t.Fatalf("park cycle %d: %v", cycle, parkErr)
		}
		ms.SetParkedAtForTest(ins.ID, now.Add(-time.Duration(40+cycle)*24*time.Hour))
		obsolete = append(obsolete, ins.ID)
	}

	recent, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceStateIf(ctx, recent.ID, string(state.StateRunning), string(state.StateParked)); err != nil {
		t.Fatal(err)
	}
	ms.SetParkedAtForTest(recent.ID, now.Add(-time.Hour))

	// WAKING is a recovery-owned state. It deliberately retains the old
	// parked_at from the prior cycle; state, rather than age alone, protects it.
	waking, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateParked), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	ms.SetParkedAtForTest(waking.ID, now.Add(-60*24*time.Hour))
	if err := store.UpdateInstanceStateIf(ctx, waking.ID, string(state.StateParked), string(state.StateWaking)); err != nil {
		t.Fatal(err)
	}

	// MIGRATING has an independent lease/recovery owner and must survive too.
	migrating, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	ms.SetParkedAtForTest(migrating.ID, now.Add(-60*24*time.Hour))
	if err := store.MarkInstanceMigrating(ctx, migrating.ID, state.DefaultLocalNodeName, "lease-retention-test"); err != nil {
		t.Fatal(err)
	}
	// A partially reconciled rollback may expose PARKED before its lease
	// metadata is cleared. Preserve that row until the migration owner lands.
	leasedParked, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkInstanceMigrating(ctx, leasedParked.ID, state.DefaultLocalNodeName, "lease-still-owned"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceStateWithTimestamp(ctx, leasedParked.ID, string(state.StateParked), now.Add(-60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	r := NewRetention(store, slog.Default()).
		WithRetention(30 * 24 * time.Hour).
		WithClock(func() time.Time { return now })
	deleted, err := r.SweepOnce(ctx)
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != len(obsolete) {
		t.Fatalf("deleted = %d, want %d obsolete park cycles", deleted, len(obsolete))
	}
	for _, id := range obsolete {
		if _, err := store.InstanceByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("obsolete parked instance %s survived: %v", id, err)
		}
	}
	for _, id := range []string{recent.ID, waking.ID, migrating.ID, leasedParked.ID} {
		if _, err := store.InstanceByID(ctx, id); err != nil {
			t.Errorf("current/recovery instance %s was deleted: %v", id, err)
		}
	}
	latest, err := store.LatestSnapshot(ctx, dep.ID)
	if err != nil {
		t.Fatalf("LatestSnapshot after parked-row retention: %v", err)
	}
	if latest.ID != snap.ID || latest.StorageKey != snap.StorageKey {
		t.Fatalf("restore snapshot changed: got %+v, want id=%s key=%s", latest, snap.ID, snap.StorageKey)
	}
}

// TestRetentionDoubleTickIsIdempotent pins the redelivery contract:
// a second sweep on the same DB finds zero rows and is a no-op.
func TestRetentionDoubleTickIsIdempotent(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	ms := retentionMemStore(t, store)
	old, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateStopped), 512, state.DefaultLocalNodeName, "")
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
	row, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateStopped), 512, state.DefaultLocalNodeName, "")
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

	old, err := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateFailed), 512, state.DefaultLocalNodeName, "")
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

type workflowRetentionCountingStore struct {
	state.WorkflowStore
	runs   int
	events int
}

func (s *workflowRetentionCountingStore) SweepExpiredWorkflowRuns(ctx context.Context, olderThan time.Duration) (int, error) {
	s.runs++
	return s.WorkflowStore.SweepExpiredWorkflowRuns(ctx, olderThan)
}

func (s *workflowRetentionCountingStore) SweepExpiredWorkflowEvents(ctx context.Context, olderThan time.Duration) (int, error) {
	s.events++
	return s.WorkflowStore.SweepExpiredWorkflowEvents(ctx, olderThan)
}

// TestLoopRunWorkflowRetentionWiresThroughLoop pins the workflow-specific
// retention ticker's dispatch seam without waiting for the hourly ticker.
func TestLoopRunWorkflowRetentionWiresThroughLoop(t *testing.T) {
	counting := &workflowRetentionCountingStore{WorkflowStore: state.NewMemStore()}
	loop := NewLoop(nil, nil, slog.Default()).WithWorkflowRetention(NewWorkflowRetention(counting, slog.Default()))

	loop.runWorkflowRetention(context.Background())

	if counting.runs != 1 || counting.events != 1 {
		t.Fatalf("workflow retention calls = runs:%d events:%d, want one each", counting.runs, counting.events)
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

	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
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
