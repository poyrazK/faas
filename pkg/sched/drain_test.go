package sched

// drain_test exercises the Move 1 event-shaped scheduler end-to-end at
// the schedd level: enqueue → drain.Tick → engine.Wake → gateway.Invoke
// → Store.CompleteInvocation. The MemStore covers the store contract;
// this file covers the lifecycle glue that turns a `pending` row into a
// `completed` row with the live instance handle stamped.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// drainSynth is a recording GatewaySynth that captures the invocations
// the drain delivers. fakeSynth in cron_test.go is package sched_test;
// this stub is in the same package as Drain so tests can construct the
// drain without an extra import.
type drainSynth struct {
	mu        sync.Mutex
	calls     atomic.Int64
	permanent atomic.Bool // if true, return ErrPermanentInvoke
	transient atomic.Bool // if true, return a transient error
}

func (d *drainSynth) SynthesizeRequest(_ context.Context, _, _, _ string) error { return nil }

func (d *drainSynth) Invoke(_ context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
	d.calls.Add(1)
	d.mu.Lock()
	perm := d.permanent.Load()
	trans := d.transient.Load()
	d.mu.Unlock()
	if perm {
		return inv, ErrPermanentInvoke
	}
	if trans {
		return inv, errors.New("drain synth: transient")
	}
	inv.State = state.InvocationDispatching
	inv.InstanceID = "inst-" + inv.ID
	return inv, nil
}

func newDrainHarness(t *testing.T, plan api.Plan, withSynth bool) (*Drain, state.Store, *fakeVMM, *fakeNotifier, *drainSynth) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	notif := &fakeNotifier{}
	_, app, _ := seedApp(t, store, plan, 256, 5)
	vmm := &fakeVMM{}
	eng := newEngine(t, store, vmm, notif, "1.10.0")
	// Wake once so an instance is parked against the app; the drain's
	// claim → wake → invoke lifecycle runs against a live instance
	// (the always-Wake path means the second call is the idempotent
	// fast path that returns the existing handle).
	if _, err := eng.Wake(ctx, app.ID); err != nil {
		t.Fatalf("seed Wake: %v", err)
	}
	// Park it so the drain's Wake goes through a real cold-boot path.
	inst, err := store.RunningInstanceForApp(ctx, app.ID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if err := store.UpdateInstanceState(ctx, inst.ID, string(state.StateParked)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	var synth GatewaySynth
	var ds *drainSynth
	if withSynth {
		ds = &drainSynth{}
		synth = ds
	}
	d := NewDrain(store, eng,
		WithDrainBatchSize(64),
		WithDrainWakeLease(60),
		WithDrainRetryAfter(5),
		WithDrainGatewaySynth(synth),
		WithDrainNotifier(notif),
		WithDrainLogger(slog.New(slog.NewTextHandler(testWriter{t}, nil))),
		WithDrainNow(func() time.Time { return time.Now().UTC() }),
	)
	return d, store, vmm, notif, ds
}

type testWriter struct{ t *testing.T }

func (tw testWriter) Write(p []byte) (int, error) { tw.t.Log(string(p)); return len(p), nil }

// seedDrainInvocation enqueues a row that is due now. Returns the row.
func seedDrainInvocation(t *testing.T, store state.Store, source state.InvocationSource) state.Invocation {
	t.Helper()
	ctx := context.Background()
	// seedApp created an app with one account; reuse it.
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	app := apps[0]
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: app.ID, AccountID: app.AccountID, Source: source,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	return inv
}

// TestDrain_DispatchesDueRow is the headline gate: one due row → one
// Invoke call → row state=completed, instance_id stamped.
func TestDrain_DispatchesDueRow(t *testing.T) {
	t.Parallel()
	d, store, _, notif, ds := newDrainHarness(t, api.PlanHobby, true)
	inv := seedDrainInvocation(t, store, state.InvocationQueue)

	d.Tick(context.Background())

	if got := ds.calls.Load(); got != 1 {
		t.Fatalf("synth calls = %d, want 1", got)
	}
	got, err := store.InvocationByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if got.State != state.InvocationCompleted {
		t.Errorf("row state = %q, want completed", got.State)
	}
	if got.InstanceID == "" {
		t.Errorf("row instance_id = empty, want stamped (meter would under-count)")
	}
	// invocation_done notify fires on the success path.
	if notif.count(db.NotifyInvocationDone) != 1 {
		t.Errorf("notify invocation_done count = %d, want 1", notif.count(db.NotifyInvocationDone))
	}
}

// TestDrain_TransientInvokeRetries pins the retryAfter=5s branch.
// A transient Invoke error puts the row back to state=pending with
// due_at in the future. A second Tick that happens BEFORE due_at must
// NOT pick the row up again.
func TestDrain_TransientInvokeRetries(t *testing.T) {
	t.Parallel()
	d, store, _, _, ds := newDrainHarness(t, api.PlanHobby, true)
	ds.transient.Store(true)
	inv := seedDrainInvocation(t, store, state.InvocationAsyncInvoke)

	// First tick: transient → FailInvocation(retryAfter=5s).
	d.Tick(context.Background())
	if got, _ := store.InvocationByID(context.Background(), inv.ID); got.State != state.InvocationPending {
		t.Fatalf("after transient fail, state = %q, want pending", got.State)
	}
	if got, _ := store.InvocationByID(context.Background(), inv.ID); !got.DueAt.After(time.Now()) {
		t.Errorf("after transient fail, due_at = %s, want in the future", got.DueAt)
	}
	if got := ds.calls.Load(); got != 1 {
		t.Errorf("first tick synth calls = %d, want 1", got)
	}

	// Second tick immediately: row is still in the future, drain must
	// skip it. attempts++ though.
	d.Tick(context.Background())
	if got := ds.calls.Load(); got != 1 {
		t.Errorf("second tick synth calls = %d, want still 1 (row not due yet)", got)
	}
}

// TestDrain_PermanentInvokeTerminates pins the retryAfter=0 branch.
// A permanent invoke error (4xx) puts the row to state=failed. No
// future tick should pick it up.
func TestDrain_PermanentInvokeTerminates(t *testing.T) {
	t.Parallel()
	d, store, _, _, ds := newDrainHarness(t, api.PlanHobby, true)
	ds.permanent.Store(true)
	inv := seedDrainInvocation(t, store, state.InvocationAsyncInvoke)

	d.Tick(context.Background())
	got, _ := store.InvocationByID(context.Background(), inv.ID)
	if got.State != state.InvocationFailed {
		t.Errorf("after permanent fail, state = %q, want failed", got.State)
	}
	if got.CompletedAt == nil {
		t.Errorf("after permanent fail, completed_at = nil, want set")
	}
	if got.LastError == "" {
		t.Errorf("after permanent fail, last_error = empty, want set")
	}

	// Second tick: failed rows are terminal, drain must not retry.
	d.Tick(context.Background())
	if got := ds.calls.Load(); got != 1 {
		t.Errorf("post-fail tick synth calls = %d, want still 1 (terminal)", got)
	}
}

// TestDrain_NotYetDueSkipped pins the (state='pending' AND due_at <= now)
// predicate. A future-dated row must NOT be picked up.
func TestDrain_NotYetDueSkipped(t *testing.T) {
	t.Parallel()
	d, store, _, _, ds := newDrainHarness(t, api.PlanHobby, true)
	apps, _ := store.ListAllApps(context.Background())
	app := apps[0]
	_, err := store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationQueue,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(1 * time.Hour),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	d.Tick(context.Background())
	if got := ds.calls.Load(); got != 0 {
		t.Errorf("synth calls = %d, want 0 (row not due yet)", got)
	}
}

// TestDrain_TenantFairnessBuckets: a 3-row backlog for app A and a
// 1-row backlog for app B in the same batch must dispatch at least one
// row for each app before the batch terminates. The current
// implementation round-robins by app_id, so a single tick on a 64-row
// batch covers the worst case.
func TestDrain_TenantFairnessBuckets(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	notif := &fakeNotifier{}
	// Two apps, same account, both woken + parked so the drain's
	// always-Wake idempotent path returns a real instance.
	acct, appA, _ := seedApp(t, store, api.PlanHobby, 256, 5)
	appB, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "appB", RAMMB: 256, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatalf("CreateApp appB: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: appB.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:b", Status: state.DeployLive,
	}); err != nil {
		t.Fatalf("CreateDeployment appB: %v", err)
	}
	vmm := &fakeVMM{}
	eng := newEngine(t, store, vmm, notif, "1.10.0")
	ds := &drainSynth{}
	d := NewDrain(store, eng,
		WithDrainGatewaySynth(ds),
		WithDrainNotifier(notif),
		WithDrainLogger(slog.New(slog.NewTextHandler(discardWriter{}, nil))),
		WithDrainNow(func() time.Time { return time.Now().UTC() }),
	)

	// Three due rows for appA, one for appB.
	for i := 0; i < 3; i++ {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{
			AppID: appA.ID, AccountID: acct.ID, Source: state.InvocationQueue,
			Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
		}); err != nil {
			t.Fatalf("seed A %d: %v", i, err)
		}
	}
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: appB.ID, AccountID: acct.ID, Source: state.InvocationQueue,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	d.Tick(ctx)
	// All 4 rows are due in this single batch, so all 4 dispatch.
	if got := ds.calls.Load(); got != 4 {
		t.Fatalf("synth calls = %d, want 4 (3 from A, 1 from B)", got)
	}
}

// TestDrain_DelayedTaskCapEnforced pins the config-drift re-check.
// delayed_task source on Hobby plan has MaxDelayedTasksPerApp=5; a
// 6th row sitting pending must be failed when the drain tries to
// dispatch it.
func TestDrain_DelayedTaskCapEnforced(t *testing.T) {
	t.Parallel()
	d, store, _, _, _ := newDrainHarness(t, api.PlanHobby, true)
	ctx := context.Background()
	apps, _ := store.ListAllApps(ctx)
	app := apps[0]
	// Hobby allows 5 pending delayed_task rows. Seed 5, then a 6th
	// must fail on dispatch.
	for i := 0; i < 5; i++ {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{
			ID: uuid.NewString(), AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationDelayedTask,
			Method: "POST", Path: "/x", DueAt: time.Now().Add(time.Duration(i+1) * time.Minute),
		}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	over, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationDelayedTask,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation over-cap: %v", err)
	}

	d.Tick(ctx)
	got, _ := store.InvocationByID(ctx, over.ID)
	// The cap re-check failed the row (retryAfter=30s to give the
	// customer a window to drain their queue).
	if got.State != state.InvocationPending {
		t.Errorf("over-cap row state = %q, want pending (with retryAfter=30s)", got.State)
	}
	if got.LastError == "" {
		t.Errorf("over-cap row last_error = empty, want set")
	}
}

// TestDrain_NoGatewayStillCompletes pins the test-seam behavior. The
// drain must still drive a row to completed when no gateway is wired
// (so the meter gets its tick in test environments).
func TestDrain_NoGatewayStillCompletes(t *testing.T) {
	t.Parallel()
	d, store, _, _, _ := newDrainHarness(t, api.PlanHobby, false)
	inv := seedDrainInvocation(t, store, state.InvocationAsyncInvoke)

	d.Tick(context.Background())
	got, _ := store.InvocationByID(context.Background(), inv.ID)
	if got.State != state.InvocationCompleted {
		t.Errorf("row state = %q, want completed (no-gateway test seam)", got.State)
	}
}

// TestDrain_ReTickIsIdempotent: a second Tick on the same batch
// (with no new rows) must not double-dispatch. The list filter
// (state='pending' AND due_at <= now) excludes completed rows.
func TestDrain_ReTickIsIdempotent(t *testing.T) {
	t.Parallel()
	d, store, _, _, ds := newDrainHarness(t, api.PlanHobby, true)
	seedDrainInvocation(t, store, state.InvocationAsyncInvoke)
	seedDrainInvocation(t, store, state.InvocationQueue)
	seedDrainInvocation(t, store, state.InvocationCron)

	d.Tick(context.Background())
	if got := ds.calls.Load(); got != 3 {
		t.Fatalf("first tick synth calls = %d, want 3", got)
	}
	d.Tick(context.Background())
	if got := ds.calls.Load(); got != 3 {
		t.Fatalf("second tick synth calls = %d, want still 3 (no re-dispatch)", got)
	}
}

// discardWriter swallows slog output during tests.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
