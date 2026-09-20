package sched

// adr: 191

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeReported is an InstanceActivityReader whose reported set the test
// controls directly. Production's *instancestats.Reader already drops
// stale samples, so "present in the map" is the whole contract this
// reconciler depends on.
type fakeReported struct {
	ids []string
}

func (f *fakeReported) SnapshotActivity(time.Time) map[string]InstanceActivity {
	if f.ids == nil {
		return nil
	}
	out := make(map[string]InstanceActivity, len(f.ids))
	for _, id := range f.ids {
		out[id] = InstanceActivity{}
	}
	return out
}

type divergenceFixture struct {
	store    *state.MemStore
	engine   *Engine
	reported *fakeReported
	rec      *InstanceDivergenceReconciler
	now      time.Time
}

// newDivergenceFixture seeds one app with n running instances on node
// "node-a", all started well outside the grace window.
func newDivergenceFixture(t *testing.T, n int) (*divergenceFixture, []state.Instance) {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 5)
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	var rows []state.Instance
	for range n {
		ins, err := store.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 256, "node-a", "")
		if err != nil {
			t.Fatalf("CreateInstance: %v", err)
		}
		rows = append(rows, ins)
	}

	reported := &fakeReported{}
	// Every seeded instance is old enough to be judged.
	now := time.Now().UTC().Add(time.Duration(api.InstanceDivergenceGraceSeconds+60) * time.Second)
	rec := NewInstanceDivergenceReconciler(engine, reported, testLog()).
		WithClock(func() time.Time { return now })

	f := &divergenceFixture{store: store, engine: engine, reported: reported, rec: rec, now: now}
	return f, rows
}

func (f *divergenceFixture) stateOf(t *testing.T, id string) string {
	t.Helper()
	ins, err := f.store.InstanceByID(context.Background(), id)
	if err != nil {
		t.Fatalf("InstanceByID %s: %v", id, err)
	}
	return ins.State
}

// A VM the owning vmmd reports is never a candidate, however many ticks run.
func TestDivergence_ReportedInstanceIsNeverTouched(t *testing.T) {
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID, rows[1].ID}

	for range 3 {
		n, err := f.rec.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if n != 0 {
			t.Fatalf("acted on %d reported instances; want 0", n)
		}
	}
	if got := f.rec.candidateCount(); got != 0 {
		t.Fatalf("candidates=%d want 0", got)
	}
}

// Confirm-twice: one absent snapshot is not enough. A single dropped
// telemetry batch must never park a live app.
func TestDivergence_RequiresTwoConsecutiveSweeps(t *testing.T) {
	f, rows := newDivergenceFixture(t, 2)
	// rows[0] is reported so node-a counts as reporting; rows[1] is not.
	f.reported.ids = []string{rows[0].ID}

	n, err := f.rec.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("first sweep acted on %d; want 0 (confirm-twice)", n)
	}
	if got := f.rec.candidateCount(); got != 1 {
		t.Fatalf("candidates after first sweep=%d want 1", got)
	}

	n, err = f.rec.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if n != 1 {
		t.Fatalf("second sweep acted on %d; want 1", n)
	}
}

// An instance that reappears between sweeps must start its confirmation over.
func TestDivergence_ReappearingInstanceClearsCandidate(t *testing.T) {
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID}

	if _, err := f.rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	if got := f.rec.candidateCount(); got != 1 {
		t.Fatalf("candidates=%d want 1", got)
	}

	// It comes back.
	f.reported.ids = []string{rows[0].ID, rows[1].ID}
	if _, err := f.rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if got := f.rec.candidateCount(); got != 0 {
		t.Fatalf("candidates after reappearance=%d want 0", got)
	}

	// It vanishes again: one sweep must not be enough.
	f.reported.ids = []string{rows[0].ID}
	n, err := f.rec.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("third Reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("acted on %d after reappearance; confirmation must restart", n)
	}
}

// A node that reported nothing is the dead-node reconciler's territory.
// This sweep must not act on it, however many times it runs.
func TestDivergence_SilentNodeIsSkipped(t *testing.T) {
	f, _ := newDivergenceFixture(t, 2)
	f.reported.ids = []string{"some-other-instance"} // non-empty, but nothing on node-a

	for range 3 {
		n, err := f.rec.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if n != 0 {
			t.Fatalf("acted on %d instances of a silent node; want 0", n)
		}
	}
}

// An empty snapshot means the poller has not ticked or the whole fleet is
// silent. Neither is evidence about any VM, and accumulated candidates
// must be cleared so they do not all fire when telemetry returns.
func TestDivergence_EmptySnapshotResetsCandidates(t *testing.T) {
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID}
	if _, err := f.rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("seed Reconcile: %v", err)
	}
	if got := f.rec.candidateCount(); got != 1 {
		t.Fatalf("candidates=%d want 1", got)
	}

	f.reported.ids = nil
	n, err := f.rec.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if n != 0 || f.rec.candidateCount() != 0 {
		t.Fatalf("acted=%d candidates=%d; an empty snapshot must reset both", n, f.rec.candidateCount())
	}
}

// A just-admitted VM may not be in the last telemetry batch. Acting on
// that race would park healthy instances during every wake burst.
func TestDivergence_GraceWindowProtectsFreshInstances(t *testing.T) {
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID}
	// Move the clock back so rows[1] is inside its grace window.
	fresh := rows[1].StartedAt.Add(time.Duration(api.InstanceDivergenceGraceSeconds-5) * time.Second)
	f.rec.WithClock(func() time.Time { return fresh })

	for range 3 {
		n, err := f.rec.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if n != 0 {
			t.Fatalf("acted on %d instances inside the grace window; want 0", n)
		}
	}
}

// Report-only is the shipped default: the counter moves, the row does not.
func TestDivergence_ReportOnlyLeavesRowsAlone(t *testing.T) {
	t.Setenv(DivergenceEnforceEnv, "")
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID}

	for range 2 {
		if _, err := f.rec.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	}
	if got := f.stateOf(t, rows[1].ID); got != string(state.StateRunning) {
		t.Fatalf("state=%q want running; report-only must not write", got)
	}
}

// Under enforcement the row lands FAILED (not PARKED: no snapshot was
// taken because the VM is gone) and the admission slot is released.
func TestDivergence_EnforceFailsRowAndReleasesLedger(t *testing.T) {
	t.Setenv(DivergenceEnforceEnv, "1")
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID}

	gone := rows[1]
	if err := f.engine.Ledger().Admit(Request{
		Instance: gone.ID, AppID: gone.AppID, Plan: api.PlanHobby, RAMMB: gone.RAMMB, NodeID: "node-a",
	}); err != nil {
		t.Fatalf("ledger Admit: %v", err)
	}
	if !f.engine.Ledger().ResidentFor(gone.ID) {
		t.Fatal("ledger reservation did not take; fixture is wrong")
	}

	for range 2 {
		if _, err := f.rec.Reconcile(context.Background()); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
	}
	if got := f.stateOf(t, gone.ID); got != string(state.StateFailed) {
		t.Fatalf("state=%q want failed", got)
	}
	if f.engine.Ledger().ResidentFor(gone.ID) {
		t.Fatal("admission slot still reserved after repair")
	}
	// The surviving instance is untouched.
	if got := f.stateOf(t, rows[0].ID); got != string(state.StateRunning) {
		t.Fatalf("reported instance state=%q want running", got)
	}
}

// A peer that moved the row first wins; the sweep counts it and does not
// error or double-write.
func TestDivergence_PeerConflictIsBenign(t *testing.T) {
	t.Setenv(DivergenceEnforceEnv, "1")
	f, rows := newDivergenceFixture(t, 2)
	f.reported.ids = []string{rows[0].ID}
	ctx := context.Background()

	if _, err := f.rec.Reconcile(ctx); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	// Peer parks the row between the two sweeps.
	if err := f.store.UpdateInstanceStateIf(ctx, rows[1].ID, string(state.StateRunning), string(state.StateParked)); err != nil {
		t.Fatalf("peer transition: %v", err)
	}

	n, err := f.rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if n != 0 {
		t.Fatalf("acted=%d want 0; the peer already moved the row", n)
	}
	if got := f.stateOf(t, rows[1].ID); got != string(state.StateParked) {
		t.Fatalf("state=%q want parked; the sweep must not overwrite a peer", got)
	}
}

// Only live states are candidates: a parked row has no VM by definition.
func TestDivergence_IgnoresNonLiveRows(t *testing.T) {
	f, rows := newDivergenceFixture(t, 2)
	ctx := context.Background()
	if err := f.store.UpdateInstanceStateIf(ctx, rows[1].ID, string(state.StateRunning), string(state.StateParked)); err != nil {
		t.Fatalf("park: %v", err)
	}
	f.reported.ids = []string{rows[0].ID}

	for range 3 {
		n, err := f.rec.Reconcile(ctx)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if n != 0 {
			t.Fatalf("acted on %d non-live rows; want 0", n)
		}
	}
}

// The per-tick cap bounds the write burst when a whole fleet diverges.
func TestDivergence_TickLimitCapsTheBatch(t *testing.T) {
	f, rows := newDivergenceFixture(t, api.InstanceDivergenceTickLimit+5)
	f.reported.ids = []string{rows[0].ID}

	if _, err := f.rec.Reconcile(context.Background()); err != nil {
		t.Fatalf("first Reconcile: %v", err)
	}
	n, err := f.rec.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	if n != api.InstanceDivergenceTickLimit {
		t.Fatalf("acted=%d want the cap %d", n, api.InstanceDivergenceTickLimit)
	}
}

// A reconciler without a reader has no evidence and must never act.
func TestDivergence_NilReaderIsInert(t *testing.T) {
	f, _ := newDivergenceFixture(t, 2)
	rec := NewInstanceDivergenceReconciler(f.engine, nil, testLog())
	n, err := rec.Reconcile(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("acted=%d err=%v; a reconciler with no reader must be inert", n, err)
	}
	var nilRec *InstanceDivergenceReconciler
	if n, err := nilRec.Reconcile(context.Background()); err != nil || n != 0 {
		t.Fatalf("nil receiver acted=%d err=%v", n, err)
	}
}

func TestDivergenceEnforceEnv(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "true": false, "1": true} {
		t.Setenv(DivergenceEnforceEnv, raw)
		if got := DivergenceEnforceEnabled(); got != want {
			t.Fatalf("%q: got %v want %v", raw, got, want)
		}
	}
}
