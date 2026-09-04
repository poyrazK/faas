// migration_harness_test.go — Tier A5 (ADR-066) end-to-end
// unit tests on the four-phase handoff orchestrated by
// MigrationHarness.MigrateOne.
//
// The state-machine half (ListLiveInstancesOnNode,
// MarkInstanceMigrating, MigrateInstanceOwner,
// CancelInstanceMigration) is pinned by
// pkg/sched/migration_handoff_test.go; this file pins the
// orchestrator: the lease-bounded context, the per-phase
// metric labels, the cancel-on-rollback discipline, and the
// per-tick cap.
//
// Failure modes pinned here:
//   - Happy path: Phase 1 → 2 → 3 → 4 commit + Phase 5 ack,
//     state ends at 'running' on the new owner, lineage
//     stamped, all four RPCs called exactly once.
//   - Phase 1 fails: no Phase 2/3/4; peer_failure bumped.
//   - Phase 2 conflict (peer re-owner / state drift): Phase 4
//     fires, conflict metric bumped.
//   - Phase 3 fails: Phase 4 fires (state→parked via
//     CancelInstanceMigration + CancelLiveMigration on the
//     dying vmmd), peer_failure bumped.
//   - Lease expiry: slow PrepareLiveMigration exceeds
//     leaseCtx; Phase 4 fires, lease_expired metric bumped.
//   - Per-tick cap: 3 instances in fixture, maxPerTick=2,
//     only 2 attempts land.
//   - Spec builder error: rolls back Phase 2 + Phase 4 fires.

package sched

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// stubSpecBuilder returns a fixed AppSpec regardless of
// instanceID. The migration_harness_test.go only asserts on
// orchestrator behaviour, not on spec contents — those are
// pinned by BuildAppSpecForMigration's own callers (the wake
// path mirrors it).
func stubSpecBuilder(_ context.Context, _ string) (AppSpec, error) {
	return AppSpec{
		BaseKey:    "base/runtime-node22.ext4",
		LayerKey:   "apps/test/dep.ext4",
		VCPUCount:  2,
		MemSizeMiB: 256,
	}, nil
}

// newHarnessForTest builds a MigrationHarness wired against a
// MemStore + the supplied fakeVMM. Lease seconds is short (2s)
// so the lease-expiry test runs quickly; maxPerTick is left at
// the api default unless the caller overrides via SetMaxPerTick.
// nodeCeilingResolver is nil by default so the harness uses the
// legacy global ceiling fallback (api.RAMAdmissionCeilingMB /
// api.VCPUSlots) — that mirrors pre-Tier-A5 test seams. Tests
// that exercise the destination's per-node ceiling thread a stub
// resolver via newHarnessForTestWithResolver.
func newHarnessForTest(t *testing.T, store *state.MemStore, vmm *fakeVMM, ownerNodeID string) *MigrationHarness {
	t.Helper()
	ops := wire.NewOpsMetrics("schedd")
	h := NewMigrationHarness(t.Context(), store, vmm, ops, testLog(), ownerNodeID, stubSpecBuilder, NewNodeLedger(), nil)
	h.SetLeaseSeconds(2)
	return h
}

// newHarnessForTestWithResolver is the variant that wires a
// resolver so the destination's per-node ceiling flows into the
// Phase 3 ledger reservation. Used by
// TestNewMigrationHarness_ThreadsDestinationCeiling.
func newHarnessForTestWithResolver(
	t *testing.T,
	store *state.MemStore,
	vmm *fakeVMM,
	ownerNodeID string,
	resolver func(ctx context.Context, nodeID string) (int, int, error),
) *MigrationHarness {
	t.Helper()
	ops := wire.NewOpsMetrics("schedd")
	h := NewMigrationHarness(t.Context(), store, vmm, ops, testLog(), ownerNodeID, stubSpecBuilder, NewNodeLedger(), resolver)
	h.SetLeaseSeconds(2)
	return h
}

func TestMigrateOne_HappyPath(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	insID := seedInstanceForMigration(t, store, "dying")
	h := newHarnessForTest(t, store, vmm, "new-owner")

	if err := h.MigrateOne(context.Background(), insID, "dying"); err != nil {
		t.Fatalf("MigrateOne happy path: %v", err)
	}

	// All four RPCs called exactly once.
	if vmm.prepares != 1 {
		t.Errorf("prepares = %d, want 1", vmm.prepares)
	}
	if vmm.adopts != 1 {
		t.Errorf("adopts = %d, want 1", vmm.adopts)
	}
	if vmm.acks != 1 {
		t.Errorf("acks = %d, want 1", vmm.acks)
	}
	if vmm.cancels != 0 {
		t.Errorf("cancels = %d, want 0 (no rollback)", vmm.cancels)
	}

	// State ended at 'running' on the new owner with lineage.
	ins, _ := store.InstanceByID(context.Background(), insID)
	if ins.NodeID != "new-owner" {
		t.Errorf("node_id = %q, want new-owner", ins.NodeID)
	}
	if string(ins.State) != string(state.StateRunning) {
		t.Errorf("state = %q, want running", ins.State)
	}
	if ins.MigratedFromNodeID == nil || *ins.MigratedFromNodeID != "dying" {
		t.Errorf("migrated_from_node_id = %v, want dying", ins.MigratedFromNodeID)
	}
	if ins.MigratedAt == nil {
		t.Errorf("migrated_at is nil")
	}
	if ins.LeaseToken == "" {
		t.Errorf("lease_token empty after commit")
	}
}

func TestMigrateOne_Phase1Fails(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{prepareErr: errors.New("Park blew up")}
	insID := seedInstanceForMigration(t, store, "dying")
	h := newHarnessForTest(t, store, vmm, "new-owner")

	err := h.MigrateOne(context.Background(), insID, "dying")
	if err == nil {
		t.Fatalf("MigrateOne: want error on Phase 1 fail, got nil")
	}
	if vmm.prepares != 0 {
		t.Errorf("prepares = %d, want 0 (Phase 1 errored before counter)", vmm.prepares)
	}
	if vmm.adopts != 0 {
		t.Errorf("adopts = %d, want 0 (Phase 3 must not run)", vmm.adopts)
	}
	if vmm.cancels != 0 {
		t.Errorf("cancels = %d, want 0 (Phase 4 must not run; Park failed before tracker put)", vmm.cancels)
	}

	// State untouched (still running on the dying node).
	ins, _ := store.InstanceByID(context.Background(), insID)
	if string(ins.State) != string(state.StateRunning) {
		t.Errorf("state = %q, want running (Phase 1 fail must not touch the row)", ins.State)
	}
}

func TestMigrateOne_Phase2Conflict(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	insID := seedInstanceForMigration(t, store, "dying")
	// Pre-mutate the row to a non-running state so MarkInstanceMigrating
	// returns ErrConflict (the predicate is state='running' +
	// node_id='dying').
	if err := store.UpdateInstanceState(context.Background(), insID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	h := newHarnessForTest(t, store, vmm, "new-owner")

	err := h.MigrateOne(context.Background(), insID, "dying")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MigrateOne on conflict = %v, want ErrConflict", err)
	}
	if vmm.prepares != 1 {
		t.Errorf("prepares = %d, want 1", vmm.prepares)
	}
	if vmm.cancels != 1 {
		t.Errorf("cancels = %d, want 1 (Phase 4 must fire on Phase 2 conflict)", vmm.cancels)
	}
	if vmm.adopts != 0 {
		t.Errorf("adopts = %d, want 0 (Phase 3 must not run)", vmm.adopts)
	}
}

func TestMigrateOne_Phase3Fails(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{adoptErr: errors.New("Restore failed on new owner")}
	insID := seedInstanceForMigration(t, store, "dying")
	h := newHarnessForTest(t, store, vmm, "new-owner")

	err := h.MigrateOne(context.Background(), insID, "dying")
	if err == nil {
		t.Fatalf("MigrateOne: want error on Phase 3 fail, got nil")
	}
	if vmm.cancels != 1 {
		t.Errorf("cancels = %d, want 1 (Phase 4 must fire on Phase 3 fail)", vmm.cancels)
	}

	// State rolled back to 'parked' (CancelInstanceMigration fired).
	ins, _ := store.InstanceByID(context.Background(), insID)
	if string(ins.State) != string(state.StateParked) {
		t.Errorf("state = %q, want parked (Phase 4 rollback)", ins.State)
	}
	if ins.NodeID != "dying" {
		t.Errorf("node_id = %q, want dying (rollback did not flip)", ins.NodeID)
	}
}

// TestMigrateOne_Phase4FailureReleasesPhase3Reservation is the
// regression test for the Phase 3 reservation leak that the
// code-review flagged in PR #726. Every Phase 4 failure branch
// (ErrConflict, ErrNotFound, lease_expired via
// context.DeadlineExceeded, generic commit failure) must call
// h.ledger.Release(instanceID) before returning, otherwise the
// destination's per-node RAM + vCPU budget stays artificially
// depressed across the next MigrateLiveInstances tick.
//
// Without the fix this test fails on the ResidentRAMForNode check
// — the destination ledger still holds the 256 MB reservation
// (stubSpecBuilder's MemSizeMiB + PerVMOverheadMB).
//
// We drive Phase 4 ErrConflict deterministically by mutating the
// instance row inside fakeVMM.adoptHook, which runs AFTER Phase 3
// succeeds (adoptRPC + ledger reservation are committed) and
// BEFORE the orchestrator runs the Phase 4 conditional UPDATE.
// The state machine's UpdateInstanceState flips the row out of
// 'migrating', so the Phase 4 predicate matches zero rows and
// MemStore returns ErrConflict. The ErrNotFound branch is
// asserted by code inspection (the Release discipline is
// identical across both branches and they share the same
// pre-Release metric bump).
func TestMigrateOne_Phase4FailureReleasesPhase3Reservation(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	insID := seedInstanceForMigration(t, store, "dying")
	// adoptHook fires AFTER AdoptMigratedInstance returns success
	// (so Phase 3's ledger reservation is committed) and BEFORE
	// the orchestrator runs the Phase 4 conditional UPDATE on
	// state='migrating' + node_id='dying'. Flip the row out of
	// 'migrating' so the predicate matches zero rows.
	vmm.adoptHook = func(_ string) {
		if err := store.UpdateInstanceState(context.Background(), insID, string(state.StateParked)); err != nil {
			t.Fatalf("UpdateInstanceState: %v", err)
		}
	}
	h := newHarnessForTest(t, store, vmm, "new-owner")

	err := h.MigrateOne(context.Background(), insID, "dying")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MigrateOne: err = %v, want errors.Is(err, ErrConflict)", err)
	}

	// Phase 4 must have fired (the dying vmmd gets the cancel hint).
	if vmm.cancels != 1 {
		t.Errorf("cancels = %d, want 1 (Phase 4 rollback)", vmm.cancels)
	}
	// Phase 5 ack must NOT have fired (the row never flipped to
	// 'running' on the destination).
	if vmm.acks != 0 {
		t.Errorf("acks = %d, want 0 (Phase 5 must not run after a Phase 4 failure)", vmm.acks)
	}
	// The Phase 3 reservation must be released. ResidentRAM for
	// the destination node returns to 0 once the orphan
	// reservation is freed; vCPU likewise; NodeCount drops to 0
	// because the per-node counter is freed when both RAM and
	// vCPU hit zero. Without the Release fix all three stay
	// positive forever.
	if got := h.ledger.ResidentRAMForNode("new-owner"); got != 0 {
		t.Errorf("ResidentRAMForNode(new-owner) = %d, want 0 (Phase 3 reservation leaked)", got)
	}
	if got := h.ledger.UsedVCPUForNode("new-owner"); got != 0 {
		t.Errorf("UsedVCPUForNode(new-owner) = %d, want 0 (Phase 3 reservation leaked)", got)
	}
	if got := h.ledger.NodeCount(); got != 0 {
		t.Errorf("NodeCount = %d, want 0 (Phase 3 reservation leaked)", got)
	}
}

// TestMigrateOne_Phase4PeerOwnerDestroysPausedSource covers the dangerous
// ownership-loss variant of the Phase 4 rollback. If a peer commits the row
// after this destination has adopted the snapshot, resuming the source would
// leave two serving VMs. The destination and the now-obsolete source must both
// be destroyed, while the peer-owned durable row remains untouched.
func TestMigrateOne_Phase4PeerOwnerDestroysPausedSource(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	insID := seedInstanceForMigration(t, store, "dying")
	vmm.adoptHook = func(_ string) {
		// The peer presents the same Phase-1 lease token that Phase 2
		// stamped on the row; only the owner changes in this simulated
		// race.
		if err := store.MigrateInstanceOwner(context.Background(), insID, "dying", "peer-owner", "lease-"+insID); err != nil {
			t.Fatalf("peer ownership commit: %v", err)
		}
	}
	h := newHarnessForTest(t, store, vmm, "new-owner")

	err := h.MigrateOne(context.Background(), insID, "dying")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MigrateOne: err = %v, want errors.Is(err, ErrConflict)", err)
	}
	if vmm.cancels != 0 {
		t.Errorf("cancels = %d, want 0 (source must not resume after peer ownership commit)", vmm.cancels)
	}
	if vmm.destroys != 2 {
		t.Errorf("destroys = %d, want 2 (destination and obsolete source cleanup)", vmm.destroys)
	}
	ins, err := store.InstanceByID(context.Background(), insID)
	if err != nil {
		t.Fatalf("reload instance: %v", err)
	}
	if ins.NodeID != "peer-owner" || ins.State != string(state.StateRunning) {
		t.Fatalf("durable row after peer commit = node=%q state=%q, want peer-owner/running", ins.NodeID, ins.State)
	}
}

// TestNewMigrationHarness_ThreadsDestinationCeiling pins the
// Gap 2 fix: the destination's per-node ceiling + vCPU budget
// must be threaded into the Phase 3 ledger reservation. Without
// this, a heterogeneous fleet with one smaller destination gets
// over-admitted (violating invariant §6.2-2).
//
// The first subtest asserts the resolver's (ceiling, vcpu) values
// land on the harness fields and that a second Admit whose RAM
// exceeds that ceiling is refused (proving the per-node gate,
// not the global fallback, was hit). The second subtest pins
// the resolver-error fallback: a broken resolver must NOT
// silently panic or refuse the migration; the harness logs +
// falls back to (0, 0) and the migration proceeds against the
// global api.RAMAdmissionCeilingMB.
func TestNewMigrationHarness_ThreadsDestinationCeiling(t *testing.T) {
	// Ceiling is set to 320 MB so stubSpecBuilder's 256 MB spec
	// (+ 8 MB overhead = 264 MB) fits on the first migration but
	// leaves 56 MB of headroom — too tight for a second 264 MB
	// admission (264 + 264 = 528 > 320). That proves the
	// per-node ceiling gate fires from the resolver-supplied
	// value rather than the global api.RAMAdmissionCeilingMB
	// (47,600 MB) fallback.
	const wantCeilingMB = 320
	const wantVCPUBudget = 4
	resolver := func(_ context.Context, nodeID string) (int, int, error) {
		if nodeID != "new-owner" {
			t.Errorf("resolver called with %q, want new-owner", nodeID)
		}
		return wantCeilingMB, wantVCPUBudget, nil
	}

	t.Run("threads_ceiling_into_reservation", func(t *testing.T) {
		store := state.NewMemStore()
		vmm := &fakeVMM{}
		insID := seedInstanceForMigration(t, store, "dying")
		h := newHarnessForTestWithResolver(t, store, vmm, "new-owner", resolver)

		if err := h.MigrateOne(context.Background(), insID, "dying"); err != nil {
			t.Fatalf("MigrateOne: %v", err)
		}
		if got := h.destinationCeilingMB; got != wantCeilingMB {
			t.Errorf("destinationCeilingMB = %d, want %d (resolver value must be threaded)",
				got, wantCeilingMB)
		}
		if got := h.destinationVCPUBudget; got != wantVCPUBudget {
			t.Errorf("destinationVCPUBudget = %d, want %d (resolver value must be threaded)",
				got, wantVCPUBudget)
		}
		// Second Admit with 260 MB — exceeds the remaining
		// 320 - 264 = 56 MB headroom (260 + 8 = 268 > 56).
		// The per-node ceiling gate must fire (not the
		// global api.RAMAdmissionCeilingMB = 47,600).
		err := h.ledger.Admit(Request{
			Instance: "refuse-2", AppID: "app-refuse", Plan: api.PlanHobby,
			RAMMB: 260, VCPU: 1, Kind: KindWake,
			NodeID: "new-owner", NodeCeilingMB: wantCeilingMB, VCPUBudget: wantVCPUBudget,
			MaxConcurrency: 5,
		})
		if err == nil {
			t.Errorf("second Admit: err = nil, want per-node ceiling refusal")
		}
	})

	t.Run("resolver_error_falls_back_to_global_defaults", func(t *testing.T) {
		store := state.NewMemStore()
		vmm := &fakeVMM{}
		brokenResolver := func(_ context.Context, _ string) (int, int, error) {
			return 0, 0, errors.New("simulated store lookup failure")
		}
		insID := seedInstanceForMigration(t, store, "dying")
		h := newHarnessForTestWithResolver(t, store, vmm, "new-owner", brokenResolver)

		if err := h.MigrateOne(context.Background(), insID, "dying"); err != nil {
			t.Fatalf("MigrateOne with broken resolver: %v (want success — fallback to global defaults)", err)
		}
		if got := h.destinationCeilingMB; got != 0 {
			t.Errorf("destinationCeilingMB = %d, want 0 (resolver failure falls back)", got)
		}
		if got := h.destinationVCPUBudget; got != 0 {
			t.Errorf("destinationVCPUBudget = %d, want 0 (resolver failure falls back)", got)
		}
	})
}

func TestMigrateOne_LeaseExpires(t *testing.T) {
	store := state.NewMemStore()
	// sleepFor > leaseSeconds (2s) so the lease-bounded context
	// fires before Phase 1 returns. fakeVMM honours ctx.Done in
	// its PrepareLiveMigration, so the call returns ctx.Err.
	vmm := &fakeVMM{sleepFor: 3 * time.Second}
	insID := seedInstanceForMigration(t, store, "dying")
	h := newHarnessForTest(t, store, vmm, "new-owner")

	err := h.MigrateOne(context.Background(), insID, "dying")
	if err == nil {
		t.Fatalf("MigrateOne: want error on lease expiry, got nil")
	}
	// State untouched: Phase 2 conditional UPDATE never fired.
	ins, _ := store.InstanceByID(context.Background(), insID)
	if string(ins.State) != string(state.StateRunning) {
		t.Errorf("state = %q, want running (lease expiry pre-Phase 2)", ins.State)
	}
}

func TestMigrateOne_SpecBuilderError(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	insID := seedInstanceForMigration(t, store, "dying")
	ops := wire.NewOpsMetrics("schedd")
	h := NewMigrationHarness(context.Background(), store, vmm, ops, testLog(), "new-owner",
		func(_ context.Context, _ string) (AppSpec, error) {
			return AppSpec{}, errors.New("simulated spec build failure")
		}, NewNodeLedger(), nil)
	h.SetLeaseSeconds(2)

	err := h.MigrateOne(context.Background(), insID, "dying")
	if err == nil {
		t.Fatalf("MigrateOne: want error on spec builder failure, got nil")
	}
	// Phase 3 never ran (adopts=0). Phase 4 fired (cancels=1).
	if vmm.adopts != 0 {
		t.Errorf("adopts = %d, want 0", vmm.adopts)
	}
	if vmm.cancels != 1 {
		t.Errorf("cancels = %d, want 1 (Phase 4 rollback)", vmm.cancels)
	}
	// State rolled back via CancelInstanceMigration.
	ins, _ := store.InstanceByID(context.Background(), insID)
	if string(ins.State) != string(state.StateParked) {
		t.Errorf("state = %q, want parked", ins.State)
	}
}

func TestMigrateOne_NilSpecBuilderPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("NewMigrationHarness(nil specBuilder) did not panic")
		}
	}()
	_ = NewMigrationHarness(context.Background(), state.NewMemStore(), &fakeVMM{}, wire.NewOpsMetrics("schedd"),
		testLog(), "new-owner", nil, NewNodeLedger(), nil)
}

// readLiveMigrationDecision scrapes the closed-set
// schedd_live_migration_decisions_total{outcome=…} counter
// from the OpsMetrics HTTP handler. Mirrors the readScaleUp
// helper at engine_test.go:408 — Prometheus pre-instantiates
// zero rows for the closed set, so a missing label returns 0.
func readLiveMigrationDecision(t *testing.T, ops *wire.OpsMetrics, outcome string) int {
	t.Helper()
	if ops == nil {
		return 0
	}
	body := getMetricsBody(t, ops)
	want := `schedd_live_migration_decisions_total{outcome="` + outcome + `"}`
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, want) {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0
			}
			n, err := strconv.Atoi(fields[len(fields)-1])
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return n
		}
	}
	return 0
}

func TestMigrateOne_MetricOutcomeMigrated(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	insID := seedInstanceForMigration(t, store, "dying")
	h := NewMigrationHarness(context.Background(), store, vmm, ops, testLog(), "new-owner", stubSpecBuilder, NewNodeLedger(), nil)
	h.SetLeaseSeconds(2)

	if err := h.MigrateOne(context.Background(), insID, "dying"); err != nil {
		t.Fatalf("MigrateOne: %v", err)
	}
	if n := readLiveMigrationDecision(t, ops, "migrated"); n != 1 {
		t.Errorf("migrated outcome = %d, want 1", n)
	}
}

func TestMigrateOne_MetricOutcomePeerFailure(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{adoptErr: errors.New("Restore failed")}
	ops := wire.NewOpsMetrics("schedd")
	insID := seedInstanceForMigration(t, store, "dying")
	h := NewMigrationHarness(context.Background(), store, vmm, ops, testLog(), "new-owner", stubSpecBuilder, NewNodeLedger(), nil)
	h.SetLeaseSeconds(2)

	if err := h.MigrateOne(context.Background(), insID, "dying"); err == nil {
		t.Fatalf("MigrateOne: want error, got nil")
	}
	if n := readLiveMigrationDecision(t, ops, "peer_failure"); n != 1 {
		t.Errorf("peer_failure outcome = %d, want 1", n)
	}
	if n := readLiveMigrationDecision(t, ops, "migrated"); n != 0 {
		t.Errorf("migrated outcome = %d, want 0", n)
	}
}

func TestMigrateOne_MetricOutcomeConflict(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	ops := wire.NewOpsMetrics("schedd")
	insID := seedInstanceForMigration(t, store, "dying")
	if err := store.UpdateInstanceState(context.Background(), insID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	h := NewMigrationHarness(context.Background(), store, vmm, ops, testLog(), "new-owner", stubSpecBuilder, NewNodeLedger(), nil)
	h.SetLeaseSeconds(2)

	if err := h.MigrateOne(context.Background(), insID, "dying"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("MigrateOne = %v, want ErrConflict", err)
	}
	if n := readLiveMigrationDecision(t, ops, "conflict"); n != 1 {
		t.Errorf("conflict outcome = %d, want 1", n)
	}
}

// TestMigrateLiveInstances_CapsPerTick drives the per-tick cap
// at the Engine level. fixture has 3 instances; maxPerTick=2
// caps to 2 attempts.
func TestMigrateLiveInstances_CapsPerTick(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	// Seed 3 running instances on a dying node.
	for i := 0; i < 3; i++ {
		seedInstanceForMigration(t, store, "dying")
	}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(wire.NewOpsMetrics("schedd")).
		WithOwnerNodeID("new-owner").WithMigrateLiveConfig(2)

	attempted, err := engine.MigrateLiveInstances(context.Background(), "dying")
	if err != nil {
		t.Fatalf("MigrateLiveInstances: %v", err)
	}
	if attempted != 2 {
		t.Errorf("attempted = %d, want 2 (per-tick cap)", attempted)
	}
	if vmm.prepares != 2 {
		t.Errorf("prepares = %d, want 2 (per-tick cap)", vmm.prepares)
	}
}

// TestMigrateLiveInstances_OwnerNodeIDEmpty confirms the
// single-box posture (no owner_node_id) is a no-op rather than
// a no-target crash.
func TestMigrateLiveInstances_OwnerNodeIDEmpty(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	seedInstanceForMigration(t, store, "dying")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	// Do NOT call WithOwnerNodeID — legacy single-box posture.
	attempted, err := engine.MigrateLiveInstances(context.Background(), "dying")
	if err != nil {
		t.Fatalf("MigrateLiveInstances: %v", err)
	}
	if attempted != 0 {
		t.Errorf("attempted = %d, want 0 (no owner_node_id)", attempted)
	}
	if vmm.prepares != 0 {
		t.Errorf("prepares = %d, want 0", vmm.prepares)
	}
}

// TestMigrateLiveInstances_DeadNodeEqualsSelf is the no-op
// branch when deadNodeID == owner_node_id (can't migrate
// from yourself to yourself).
func TestMigrateLiveInstances_DeadNodeEqualsSelf(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	seedInstanceForMigration(t, store, "self")
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(wire.NewOpsMetrics("schedd")).
		WithOwnerNodeID("self")
	attempted, err := engine.MigrateLiveInstances(context.Background(), "self")
	if err != nil {
		t.Fatalf("MigrateLiveInstances: %v", err)
	}
	if attempted != 0 {
		t.Errorf("attempted = %d, want 0", attempted)
	}
}

// TestBuildAppSpecForMigration_NonEmpty is a smoke guard
// against a future refactor that breaks
// BuildAppSpecForMigration's signature. The harness tests above
// use the stubSpecBuilder, so a regression in the canonical
// builder wouldn't surface from those alone.
//
// Seeds the full instance + app + deployment + account chain
// the wake-time builder relies on, then asserts the migration
// builder produces a spec with non-empty drive0 base, drive1
// layer, VCPU count, and matching memory.
func TestBuildAppSpecForMigration_NonEmpty(t *testing.T) {
	store := state.NewMemStore()
	insID := seedInstanceForMigration(t, store, "dying")

	// seedInstanceForMigration seeds account + app + instance
	// but not deployment. Resolve the app and seed its live
	// deployment so BuildAppSpecForMigration's
	// LiveDeployment call doesn't 404.
	ins, err := store.InstanceByID(context.Background(), insID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	depID := "dep-" + uuid.NewString()
	if _, err := store.CreateDeployment(context.Background(),
		state.Deployment{ID: depID, AppID: ins.AppID, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:seed", Status: state.DeployLive, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
		WithOpsMetrics(wire.NewOpsMetrics("schedd"))
	spec, err := engine.BuildAppSpecForMigration(context.Background(), insID)
	if err != nil {
		t.Fatalf("BuildAppSpecForMigration: %v", err)
	}
	if spec.BaseKey == "" {
		t.Errorf("BaseKey empty; want base/runtime-node22.ext4 (driven by app.Runtime)")
	}
	if spec.LayerKey == "" {
		t.Errorf("LayerKey empty; want apps/<slug>/<depID>.ext4")
	}
	if spec.VCPUCount == 0 {
		t.Errorf("VCPUCount=0; want the per-plan VCPU (Hobby=2)")
	}
	if spec.MemSizeMiB != 256 {
		t.Errorf("MemSizeMiB = %d, want 256 (seeded)", spec.MemSizeMiB)
	}
	if spec.EgressMbit == 0 {
		t.Errorf("EgressMbit=0; want the per-plan cap (Hobby=25)")
	}
}
