// Engine-level tests for the operator-initiated ForceRestart
// primitive (P2d / PR #1105 follow-on). Mirrors the structural
// surface of pkg/sched/engine_liveness_test.go (AC envelope:
// destroy-side snap flip + state transition) but pins the
// operator-path contract:
//
//   - On a RUNNING instance: destroy fires, both warm + init
//     snaps flip stale, instance transitions to STOPPED, and
//     the returned snap IDs are non-empty (the operator UI
//     uses them to render "what was affected").
//   - On a non-RUNNING instance (WAKING, COLD_BOOTING, PARKED,
//     STOPPED): returns state.ErrInstanceNotRunning. No destroy
//     is attempted, no transition is written. The race-loser
//     posture — the customer-driven action won the lock.
//   - On a missing instance row: returns a wrapped
//     state.ErrNotFound without acquiring any lock. The apid
//     handler maps this to a 404 instance_not_found (404 is the
//     right semantic; the row really doesn't exist).
//   - On a Destroy error: returns the wrapped destroy error
//     AND the snap IDs marked stale. The snap-stale work is
//     durable; the destroy failure is what the operator sees
//     on the wire. Subscriber stamps the operator_intent row
//     failed.
//
// We intentionally drive Engine.ForceRestart directly rather
// than going through the operator_intent subscriber (that's
// pinned by operator_intent_subscriber_test.go). The Engine
// surface is the load-bearing contract.
package sched_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestForceRestart_HappyPath pins the success shape: a RUNNING
// instance is destroyed, both warm + init snapshots flip stale,
// the instance row transitions to STOPPED, and the returned snap
// IDs include both tiers (in warm-then-init order).
func TestForceRestart_HappyPath(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

	// Seed warm + init snapshots so the snap-stale loop has
	// something to flip.
	warm, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 1 << 20, DiskBytes: 1 << 20, StorageKey: "/tmp/warm",
		Tier: state.SnapshotTierWarm,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot warm: %v", err)
	}
	_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 1 << 20, DiskBytes: 1 << 20, StorageKey: "/tmp/init",
		Tier: state.SnapshotTierInit,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot init: %v", err)
	}

	inst := sched.RunningInstance(t, store, app, dep, vmm, engine)

	snapIDs, err := engine.ForceRestart(context.Background(), inst.ID, "operator_smoke")
	if err != nil {
		t.Fatalf("ForceRestart: %v", err)
	}
	if len(snapIDs) != 2 {
		t.Errorf("ForceRestart returned %d snap IDs, want 2 (warm + init)", len(snapIDs))
	}
	// Warm-first ordering matches the engine's snap-stale loop
	// (state.SnapshotTierWarm then state.SnapshotTierInit). The
	// IDs are uuid-4 strings, so just check membership + order.
	if len(snapIDs) == 2 && snapIDs[0] != warm.ID {
		t.Errorf("snapIDs[0] = %q, want warm.ID %q (ordering must be warm-then-init)", snapIDs[0], warm.ID)
	}

	// Destroy was called exactly once on vmm.
	if (*vmm.Destroys()) != 1 {
		t.Errorf("vmm.destroys = %d, want 1 (ForceRestart must call Destroy exactly once)", (*vmm.Destroys()))
	}

	// Instance row must be STOPPED.
	final, err := store.InstanceByID(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.State(final.State) != state.StateStopped {
		t.Errorf("instance.State = %q, want %q", final.State, state.StateStopped)
	}

	// LatestSnapshotForTier on either tier must now ErrNotFound
	// (post-mark-stale). This is the AC #4 / ADR-005 guarantee
	// the next wake cold-boots instead of restoring.
	_, err = store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierWarm)
	if err == nil {
		t.Errorf("LatestSnapshotForTier(warm) = nil, want ErrNotFound (next wake must cold-boot)")
	}
	_, err = store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err == nil {
		t.Errorf("LatestSnapshotForTier(init) = nil, want ErrNotFound (next wake must cold-boot)")
	}
}

// TestForceRestart_RaceLoser pins the WAKING-at-lock-acquire
// state: the customer-driven Wake raced the operator's
// force-restart and the locked re-read observes WAKING (not
// RUNNING). Returns state.ErrInstanceNotRunning; no destroy is
// attempted, no transition is written. The subscriber
// (operator_intent_subscriber_test.go) verifies the audit-row
// fan-out for this branch.
func TestForceRestart_RaceLoser(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

	// Create a WAKING instance directly — no runningInstance
	// helper because that creates a RUNNING row. The state-gate
	// rejects WAKING.
	inst, err := store.CreateInstance(context.Background(), "app-id", dep.ID, string(state.StateWaking), 512, state.DefaultLocalNodeName, "")
	if err != nil {
		t.Fatalf("CreateInstance(WAKING): %v", err)
	}

	_, err = engine.ForceRestart(context.Background(), inst.ID, "operator_smoke")
	if err == nil {
		t.Fatal("ForceRestart(WAKING) = nil, want state.ErrInstanceNotRunning")
	}
	if !errors.Is(err, state.ErrInstanceNotRunning) {
		t.Errorf("ForceRestart(WAKING) err = %v, want state.ErrInstanceNotRunning", err)
	}

	// No destroy was attempted.
	if (*vmm.Destroys()) != 0 {
		t.Errorf("vmm.destroys = %d, want 0 (race-loser must NOT destroy)", (*vmm.Destroys()))
	}

	// State is unchanged (still WAKING) — no transition was written.
	final, err := store.InstanceByID(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.State(final.State) != state.StateWaking {
		t.Errorf("instance.State = %q, want %q (race-loser must not transition)", final.State, state.StateWaking)
	}
}

// TestForceRestart_NonExistent pins the not-found path: an
// instance_id that doesn't exist returns a wrapped
// state.ErrNotFound. No lock is acquired, no error leak.
func TestForceRestart_NonExistent(t *testing.T) {
	store := state.NewMemStore()
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")

	_, err := engine.ForceRestart(context.Background(), "ghost-instance-id", "operator_smoke")
	if err == nil {
		t.Fatal("ForceRestart(ghost) = nil, want wrapped ErrNotFound")
	}
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ForceRestart(ghost) err = %v, want state.ErrNotFound in chain", err)
	}
	if !strings.Contains(err.Error(), "force_restart") {
		t.Errorf("err = %q, want substring %q (operation context, pkg/api/errors.go convention)", err.Error(), "force_restart")
	}

	// No destroy attempted.
	if (*vmm.Destroys()) != 0 {
		t.Errorf("vmm.destroys = %d, want 0 (non-existent must not destroy)", (*vmm.Destroys()))
	}
}

// TestForceRestart_DestroyFailure pins the partial-success
// shape: the snap-stale work completes, the destroy fails,
// ForceRestart returns (snapIDs, err). The operator learns two
// things from the wire: snaps were flipped (durable signal) AND
// the destroy did not complete (operator-visible error). The
// subscriber stamps the operator_intent row failed; the next
// wake still cold-boots because the snaps are stale.
func TestForceRestart_DestroyFailure(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := sched.SeedApp(t, store, api.PlanPro, 512, 5)
	vmm := &sched.FakeVMM{}
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	(*

	// Inject a destroy error so the path returns the destroy
	// error wrapped. The snap-stale work runs first (the loop
	// precedes the destroy); the snap IDs are returned to the
	// caller even though the destroy failed.
	vmm.DestroyErr()) = errors.New("fake: firecracker wedged")

	warm, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 1 << 20, DiskBytes: 1 << 20, StorageKey: "/tmp/warm",
		Tier: state.SnapshotTierWarm,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot warm: %v", err)
	}

	inst := sched.RunningInstance(t, store, app, dep, vmm, engine)

	snapIDs, err := engine.ForceRestart(context.Background(), inst.ID, "operator_smoke")
	if err == nil {
		t.Fatal("ForceRestart(destroyErr) = nil, want wrapped destroy error")
	}
	if !strings.Contains(err.Error(), "force_restart") {
		t.Errorf("err = %q, want substring %q (operation context)", err.Error(), "force_restart")
	}
	if !strings.Contains(err.Error(), "wedged") {
		t.Errorf("err = %q, want substring %q (destroy cause must surface)", err.Error(), "wedged")
	}

	// Snap IDs were returned EVEN THOUGH the destroy failed —
	// the snap-stale work is durable, the caller wants to
	// know "what was affected". Membership + non-empty is
	// enough (uuid-4 ordering is not load-bearing here).
	if len(snapIDs) != 1 || snapIDs[0] != warm.ID {
		t.Errorf("snapIDs = %v, want [%q] (snap-stale work is durable through destroy failure)", snapIDs, warm.ID)
	}

	// The destroy was attempted (and failed). We can't tell from
	// fakeVMM alone whether the call happened on the failure
	// branch vs. succeeded — fakeVMM.destroys increments only on
	// success. Confirm the underlying assumption: the destroy
	// path was entered by reading the snap post-failure (must be
	// stale, not pre-failure).
	_, err = store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierWarm)
	if err == nil {
		t.Errorf("LatestSnapshotForTier(warm) (post-destroy-fail) = nil, want ErrNotFound (snap-stale work must run before destroy)")
	}

	// Instance row is NOT STOPPED — the destroy failed, so the
	// transitionWithKind line never executes (the destroy
	// failure returns before it). The caller (subscriber)
	// stamps the operator_intent row failed; the snap-stale
	// work + the failed-destroy record are the durable
	// operator signal that the action was partial.
	final, err := store.InstanceByID(context.Background(), inst.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if state.State(final.State) != state.StateRunning {
		t.Errorf("instance.State = %q, want %q (destroy failed → no transition)", final.State, state.StateRunning)
	}
}
