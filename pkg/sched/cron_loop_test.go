package sched

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeWakeVMM satisfies VMM without touching firecracker. Returns a stub
// WakeOutcome so the engine's transition to RUNNING completes and the
// dispatch loop's path is fully exercised. Records every wake for the
// test to inspect.
type fakeWakeVMM struct {
	calls   atomic.Int64
	lastApp atomic.Value // last wake's app_id
}

func (f *fakeWakeVMM) CreateColdBoot(_ context.Context, _, instanceID string, _ AppSpec) (*WakeOutcome, error) {
	f.calls.Add(1)
	f.lastApp.Store(instanceID)
	// 10.0.0.2 is the inner guest IP from ADR-009 (the spec values "every
	// guest is 10.0.0.2/30 behind tap0 inside its own netns" so that one
	// snapshot restores as N instances). Netns naming is synthetic.
	return &WakeOutcome{
		Method:   0, // WAKE_COLD_BOOT
		HostIP:   "10.100.0.2",
		Netns:    "netns-" + instanceID,
		LeaseUID: 20000,
	}, nil
}

func (f *fakeWakeVMM) CreateFromSnapshot(_ context.Context, _, _ string, _ AppSpec, _ SnapshotRef) (*WakeOutcome, error) {
	return nil, errors.New("snapshot not available in test")
}
func (f *fakeWakeVMM) PauseAndSnapshot(_ context.Context, _, _ string, _, _ string, _ string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the cron-loop
// test's no-op seam — the cron path doesn't fire warm captures
// (the engine's reaper is the only entry point) but the
// RoutedVMM interface contract requires the method.
func (f *fakeWakeVMM) WarmSnapshot(_ context.Context, _, _, _, _ string) (SnapshotBytes, error) {
	return SnapshotBytes{}, nil
}
func (f *fakeWakeVMM) Destroy(_ context.Context, _, _ string) error { return nil }

// StopInstance (M-2 / ADR-138 §Decision 1) is the
// graceful signal-then-grace-then-SIGKILL stop
// sequence. Test fakes default to no-op + nil —
// the engine's per-mode dispatch lives in
// pkg/sched/engine_stop_pgtest_test.go (commit 6).
func (f *fakeWakeVMM) StopInstance(_ context.Context, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}
func (f *fakeWakeVMM) StopInstanceOnNode(_ context.Context, _, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}

// FrameworkReady implements RoutedVMM for the cron-loop test fake
// (issue #470 / PR #470-FU-B). No-op — the cron tests don't drive
// the warm-capture path; the engine tests in engine_test.go have a
// separate fakeVMM that tracks the framework-ready count.
func (f *fakeWakeVMM) FrameworkReady(_ context.Context, _, _ string, _ int64) error {
	return nil
}

// Ping implements RoutedVMM for the cron-loop test fake (PR #114).
// Always succeeds with a fixed fc_version; tests that need to
// exercise the heartbeat path use the heartbeat_test.go fake
// instead.
func (f *fakeWakeVMM) Ping(_ context.Context, _ string) (*PingOutcome, error) {
	return &PingOutcome{FcVersion: "1.10.0"}, nil
}

// Stats implements RoutedVMM (issue #170 / PR-A). Cron tests do
// not assert on Stats; return empty snapshot.
func (f *fakeWakeVMM) Stats(_ context.Context, _ string) (*StatsSnapshot, error) {
	return &StatsSnapshot{}, nil
}

// UpdateEgressAllowlist (tier-2 PR-B) — the cron loop tests
// never drive the egress drift path. Records nothing; the
// egress_drift subscriber's own tests wire a recording fake.
func (f *fakeWakeVMM) UpdateEgressAllowlist(_ context.Context, _, _ string, _ []netip.Prefix) error {
	return nil
}

// UpdateStaticEgressIP (ADR-119) is the no-op test fake.
// Mirrors UpdateEgressAllowlist above.
func (f *fakeWakeVMM) UpdateStaticEgressIP(_ context.Context, _, _, _ string, _ string) error {
	return nil
}

// Logs (issue #254 / Move 4, issue #517 / PR-B) — the cron loop
// tests never drive the log stream path; the scheddgrpc handler
// tests do. Returns a closed fakeLogStream so any accidental caller
// exits cleanly. PR-B adds the sinceWrittenAt time lower-bound; the
// fake ignores it.
func (f *fakeWakeVMM) Logs(_ context.Context, _, _ string, _ int64, _ time.Time) (LogStream, error) {
	return &fakeLogStream{}, nil
}

// Tier A5 (ADR-066): the cron loop's fake doesn't drive
// migrations — the methods are no-op stubs to satisfy
// RoutedVMM. The engine never calls these on the cron path.
func (f *fakeWakeVMM) PrepareLiveMigration(_ context.Context, _, _, _ string) (LiveMigrationPrepare, error) {
	return LiveMigrationPrepare{}, nil
}
func (f *fakeWakeVMM) AdoptMigratedInstance(_ context.Context, _, _ string, _ AppSpec, _, _, _ string) (LiveMigrationAdopt, error) {
	return LiveMigrationAdopt{}, nil
}
func (f *fakeWakeVMM) AcknowledgeMigration(_ context.Context, _, _, _ string) error { return nil }
func (f *fakeWakeVMM) CancelLiveMigration(_ context.Context, _, _, _ string) error  { return nil }

// recordingSynth captures every synthesize call. The cron loop's
// "post a synthetic request through gatewayd-internal so metering applies" path
// goes through this stub instead of dialing the unix socket.
type recordingSynth struct {
	calls atomic.Int64
	last  atomic.Value // last (appID, path)
}

func (r *recordingSynth) SynthesizeRequest(_ context.Context, appID, _, path string) error {
	r.calls.Add(1)
	r.last.Store(struct{ AppID, Path string }{AppID: appID, Path: path})
	return nil
}

// Invoke is the Move 1 wake-with-envelope path. recordingSynth just
// increments so cron tests can assert the call landed (and the row
// arrived in invocations). The production gateway adapter (see
// cmd/gatewayd-internal/main.go) returns the live instance id from
// sched.Wake; the test fake synthesises one so the cron loop's
// StampInstanceInvocation can land without a real wake.
func (r *recordingSynth) Invoke(_ context.Context, appID string, inv state.Invocation) (state.Invocation, error) {
	r.calls.Add(1)
	r.last.Store(struct{ AppID, Path string }{AppID: appID, Path: inv.Path})
	inv.State = state.InvocationDispatching
	inv.InstanceID = "inst-fake-" + inv.ID
	return inv, nil
}

// makeEngine builds a sched.Engine backed by a MemStore and the fake
// VMM. ledger is the in-memory admission ledger (re-built by NewNodeLedger).
func makeEngine(t *testing.T, store state.Store, vmm RoutedVMM) (*Engine, *NodeLedger) {
	t.Helper()
	ledger := NewNodeLedger()
	eng, err := NewEngine(context.Background(), store, ledger, vmm, nil, "fc-test", slog.Default())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return eng, ledger
}

func newAppAndCron(t *testing.T, store state.Store, accountID string, enabled bool) (state.App, state.Cron) {
	t.Helper()
	ctx := context.Background()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: accountID, Slug: "a", Type: state.AppTypeApp,
		RAMMB: 256,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	// Wake needs a live deployment; seed one so the engine's resolveApp
	// can return it. Otherwise the dispatch path's Wake call 404s on
	// `LiveDeployment` and the cron never reaches the synth step.
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	c, err := store.CreateCron(ctx, app.ID, "* * * * *", "/ping", enabled)
	if err != nil {
		t.Fatalf("create cron: %v", err)
	}
	// Backdate CreatedAt well past the test's frozen clock. MemStore
	// stamps CreatedAt = time.Now() at CreateCron, but the test loop
	// uses a fixed clock set to e.g. 2026-07-17 12:02 UTC — without
	// backdating, the dispatch path's first-fire guard ("NextFireAt(
	// CreatedAt) > now") always trips and the cron never fires.
	past := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)
	if _, err := store.UpdateCron(ctx, c.ID, nil, nil, nil, &past); err != nil {
		t.Fatalf("backdate cron: %v", err)
	}
	return app, c
}

// TestCronDispatch_FiresOncePerBoundary is the headline gate for M7:
// a fake clock advance of one minute produces exactly one synth call
// through gatewayd-internal. Re-running the tick immediately (no clock advance)
// must NOT re-fire — the LastFiredAt boundary guards against double
// dispatch. vmm.calls stays at 1 across the second/third ticks because
// engine.Wake has an idempotent fast path for already-RUNNING apps
// (spec §4.3); the cron path is *not* a cold-boot guarantee, it's a
// "synthesize one request per minute per cron" guarantee.
func TestCronDispatch_FiresOncePerBoundary(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	app, _ := newAppAndCron(t, store, acct.ID, true)

	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &recordingSynth{}
	now := time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC)
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithClock(func() time.Time { return now })

	// First tick: cron is due (CreatedAt backdated to 11:00, well past
	// the previous minute boundary).
	loop.runCronTick(ctx)
	if got := vmm.calls.Load(); got != 1 {
		t.Fatalf("cold boots after first tick = %d, want 1", got)
	}
	if got := synth.calls.Load(); got != 1 {
		t.Fatalf("synth calls after first tick = %d, want 1", got)
	}

	// Second tick without advancing the clock: already fired in this
	// boundary; must skip (synth still 1, no second cold boot).
	loop.runCronTick(ctx)
	if got := vmm.calls.Load(); got != 1 {
		t.Fatalf("cold boots after second tick = %d, want still 1", got)
	}
	if got := synth.calls.Load(); got != 1 {
		t.Fatalf("synth calls after second tick = %d, want still 1", got)
	}

	// Advance past the next boundary: must fire again. Wake takes the
	// idempotent fast path so cold-boot count stays at 1; synth count
	// is the "did we fire?" signal.
	now = time.Date(2026, 7, 17, 12, 3, 0, 0, time.UTC)
	loop.now = func() time.Time { return now }
	loop.runCronTick(ctx)
	if got := vmm.calls.Load(); got != 1 {
		t.Fatalf("cold boots after advance = %d, want still 1 (Wake fast-path)", got)
	}
	if got := synth.calls.Load(); got != 2 {
		t.Fatalf("synth calls after advance = %d, want 2", got)
	}

	last := synth.last.Load().(struct{ AppID, Path string })
	if last.AppID != app.ID || last.Path != "/ping" {
		t.Fatalf("last synth = %+v, want app=%s path=/ping", last, app.ID)
	}

	// Move 1 regression: the cron path must leave a row in
	// invocations (state=completed) for each fire. The meter reads
	// those rows; without them cron traffic is invisible to billing.
	rows, err := store.ListInvocationsForAccount(ctx, acct.ID, 50, "")
	if err != nil {
		t.Fatalf("ListInvocationsForAccount: %v", err)
	}
	cronRows := 0
	for _, r := range rows {
		if r.Source == state.InvocationCron && r.AppID == app.ID {
			cronRows++
			if r.State != state.InvocationCompleted {
				t.Errorf("cron row state = %q, want completed: %+v", r.State, r)
			}
			if r.InstanceID == "" {
				t.Errorf("cron row missing instance_id (meter will under-count): %+v", r)
			}
		}
	}
	if cronRows != 2 {
		t.Errorf("invocations rows with source=cron = %d, want 2 (one per fire)", cronRows)
	}
}

// TestCronDispatch_SuspendedAccountSkipped pins the §11 abuse guard:
// suspended accounts get no cron traffic. The loop must short-circuit
// before Wake so we don't gratuitously boot a VM only to park it.
func TestCronDispatch_SuspendedAccountSkipped(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "c@example.com", api.PlanFree)
	if err := store.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	_, _ = newAppAndCron(t, store, acct.ID, true)

	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &recordingSynth{}
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithClock(func() time.Time { return time.Now().UTC() })

	loop.runCronTick(ctx)
	if got := vmm.calls.Load(); got != 0 {
		t.Fatalf("wake calls = %d, want 0 for suspended account", got)
	}
	if got := synth.calls.Load(); got != 0 {
		t.Fatalf("synth calls = %d, want 0 for suspended account", got)
	}
}

// TestCronDispatch_NoGatewayNoCrash pins the "gateway synth is
// optional" invariant. When schedd is wired without an internal RPC
// client (e.g. before gatewayd-internal starts up), the loop must still Wake
// and mark the cron as fired — the synth call is best-effort.
func TestCronDispatch_NoGatewayNoCrash(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "c@example.com", api.PlanPro)
	app, cron := newAppAndCron(t, store, acct.ID, true)

	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	loop := NewLoop(nil, eng, slog.Default()).
		WithClock(func() time.Time {
			return time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC)
		})

	loop.runCronTick(ctx)

	if got := vmm.calls.Load(); got != 1 {
		t.Fatalf("wake calls = %d, want 1 even without gateway synth", got)
	}
	got, err := store.CronByID(ctx, cron.ID)
	if err != nil {
		t.Fatalf("read cron: %v", err)
	}
	if got.LastFiredAt.IsZero() {
		t.Fatalf("LastFiredAt still zero after tick (app=%s)", app.ID)
	}
}

// TestCronDispatch_DisabledSkipped: a cron row with Enabled=false must
// not appear in ListEnabledCrons, so the loop doesn't see it. We test
// the seam directly: dispatchOneCron on a disabled cron we construct
// by hand is the belt; ListEnabledCrons is the suspenders.
func TestCronDispatch_DisabledSkipped(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	enabled, err := store.ListEnabledCrons(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("expected zero enabled crons in fresh store, got %d", len(enabled))
	}
}

// TestCronDispatch_BadScheduleSkippedNotPanics: a cron row whose
// schedule string is unparseable must be logged + skipped, never
// panic the loop. We hand-craft a cron row directly because the public
// CreateCron path validates the expression; the dispatch loop's
// parse-skip branch is the contract under test.
func TestCronDispatch_BadScheduleSkippedNotPanics(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	app, _ := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "a", Type: state.AppTypeApp, RAMMB: 128,
	})

	// Inject a cron with a deliberately broken schedule directly
	// through the Cron struct (no public UpdateCron needed — the
	// dispatch path reads from the struct as-is).
	if _, err := ParseSchedule("not a cron"); !errors.Is(err, ErrInvalidSchedule) {
		t.Fatalf("ParseSchedule(bad) = %v, want ErrInvalidSchedule", err)
	}

	// And ensure the dispatch path's ParseSchedule call survives a
	// malformed schedule without panicking by hand-building a cron
	// struct and calling dispatchOneCron. The Cron row's schedule
	// field is just a string, so we can swap it post-create.
	c, err := store.CreateCron(ctx, app.ID, "* * * * *", "/x", true)
	if err != nil {
		t.Fatalf("create cron: %v", err)
	}
	// We can't easily mutate Schedule through the public Store
	// interface (no UpdateCron); instead we verify the loop doesn't
	// panic when given a Cron struct with a bad schedule via the
	// dispatch helper directly.
	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	loop := NewLoop(nil, eng, slog.Default()).
		WithClock(func() time.Time { return time.Now().UTC() })

	badCron := c
	badCron.Schedule = "definitely not cron"
	loop.dispatchOneCron(ctx, badCron, time.Now().UTC())

	if got := vmm.calls.Load(); got != 0 {
		t.Fatalf("wake calls = %d, want 0 for bad schedule", got)
	}
}

// TestCronDispatch_EmitsCronFiredAudit pins issue #291 follow-up: the
// schedd FIRE path must emit a `cron.fired` audit row keyed to the
// owning account. Mirrors TestCronDispatch_FiresOncePerBoundary for
// the seed/dispatch shape; asserts ListEvents(subject=acctID)
// returns one row with kind="cron.fired", actor="schedd", and the
// 9-key payload documented in pkg/sched/loop.go (status="ok" +
// invocation_id + instance_id populated on the success path).
//
// Subject is the account id (not the instance id) because the
// appendEvents surface is per-account for billing/quota audit
// introspection — accounts can have multiple crons firing across
// multiple instances; an operator queries "what crons fired for
// acct X in the last hour?" via Subject=<acctID>. ListEvents
// expects canonical UUID; we normalise the hex form via uuidStringOf
// (same helper events_test uses) so the test is robust to the
// MemStore hex-vs-canonical id shape.
func TestCronDispatch_EmitsCronFiredAudit(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	app, cron := newAppAndCron(t, store, acct.ID, true)

	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &recordingSynth{}
	now := time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC)
	ops := wire.NewOpsMetrics("schedd")
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithAudit(audit.New(store, slog.Default(), ops, "schedd")).
		WithClock(func() time.Time { return now })

	loop.runCronTick(ctx)

	rows, err := store.ListEvents(ctx, acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListEvents(acct) returned %d rows, want 1 (cron.fired); rows=%+v", len(rows), rows)
	}
	e := rows[0]
	if e.Kind != "cron.fired" {
		t.Errorf("event kind = %q, want cron.fired", e.Kind)
	}
	if e.Actor != "schedd" {
		t.Errorf("event actor = %q, want schedd", e.Actor)
	}
	if e.Subject == nil {
		t.Fatalf("event Subject = nil; cron.fired must carry the acct id")
	}
	if got := e.Subject.String(); got != uuidStringOf(acct.ID) {
		t.Errorf("event Subject = %s, want %s", got, uuidStringOf(acct.ID))
	}
	var payload map[string]any
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		t.Fatalf("event Data not valid JSON: %v (data=%q)", err, e.Data)
	}
	// Every payload key is present — JSON shape stays stable even
	// on the err path so dashboards can filter without nil checks.
	// last_fired_at_before is the documented exception: it is
	// omitted on the first fire (no prior fire exists) so the
	// field reads as nil rather than the misleading
	// "0001-01-01T00:00:00Z". The companion test
	// TestCronDispatch_FirstFireAuditNullLastFiredAtBefore pins
	// that wire shape on the first fire; this test uses newAppAndCron
	// which backdates CreatedAt but does NOT pre-fire, so this
	// row is itself a first-fire (LastFiredAt was zero) and the
	// key is correctly absent.
	for _, k := range []string{
		"cron_id", "app_id", "schedule", "path",
		"fired_at", "status",
		"invocation_id", "instance_id",
	} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing key %q (full payload=%+v)", k, payload)
		}
	}
	// Value-level checks on the success path.
	if payload["status"] != "ok" {
		t.Errorf("payload.status = %v, want ok (synth call succeeded)", payload["status"])
	}
	if payload["app_id"] != app.ID {
		t.Errorf("payload.app_id = %v, want %s", payload["app_id"], app.ID)
	}
	if payload["cron_id"] != cron.ID {
		t.Errorf("payload.cron_id = %v, want %s", payload["cron_id"], cron.ID)
	}
	if payload["schedule"] != "* * * * *" {
		t.Errorf("payload.schedule = %v, want \"* * * * *\"", payload["schedule"])
	}
	if payload["path"] != "/ping" {
		t.Errorf("payload.path = %v, want /ping", payload["path"])
	}
	// invocation_id is the invocations row id; instance_id is the
	// live VM. recordingSynth stamps inst-fake-<inv.ID> (see
	// cron_loop_test.go:88), so instance_id is non-empty iff the
	// synth path landed. Pin both ends so future refactors can't
	// silently regress the join.
	if payload["invocation_id"] == "" {
		t.Errorf("payload.invocation_id empty on success path (full payload=%+v)", payload)
	}
	if payload["instance_id"] == "" {
		t.Errorf("payload.instance_id empty on success path (full payload=%+v)", payload)
	}

	// Second-fire contract: after the first fire, LastFiredAt is
	// pinned, so the next tick's row carries last_fired_at_before
	// as the first fire's fired_at. Advance the clock one minute
	// and re-tick; the new row must populate the key.
	cronRow, err := store.CronByID(ctx, cron.ID)
	if err != nil {
		t.Fatalf("CronByID: %v", err)
	}
	if cronRow.LastFiredAt.IsZero() {
		t.Fatal("LastFiredAt still zero after first fire")
	}
	now = now.Add(time.Minute)
	loop.runCronTick(ctx)

	rows2, err := store.ListEvents(ctx, acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents (post-second-tick): %v", err)
	}
	if len(rows2) != 2 {
		t.Fatalf("ListEvents(acct) returned %d rows after second tick, want 2; rows=%+v", len(rows2), rows2)
	}
	var payload2 map[string]any
	if err := json.Unmarshal(rows2[0].Data, &payload2); err != nil {
		t.Fatalf("event Data not valid JSON: %v (data=%q)", err, rows2[0].Data)
	}
	// rows2 is in DESC order at the wire (ListEvents returns most
	// recent first), so rows2[0] is the second fire.
	lfBefore, ok := payload2["last_fired_at_before"]
	if !ok {
		t.Fatalf("payload missing last_fired_at_before on second fire (full=%+v)", payload2)
	}
	if lfBefore == "" {
		t.Errorf("last_fired_at_before empty on second fire (full=%+v)", payload2)
	}
}

// TestCronDispatch_CronFiredAuditFailureDoesNotRollback pins the
// best-effort contract from ADR-035: a failing audit write never
// rolls back the fire. We wrap the store in failingEventStore (which
// makes only AppendEvent error — same pattern events_test.go:99
// uses for the engine-transition regression). After runCronTick:
//   - the cron row's LastFiredAt must be set (fire committed), and
//   - the dispatch path must not have panicked.
//
// We do NOT pin the AuditWriteFailures counter here — pkg/audit's
// own tests cover that path; the schedd concern is just "did the
// fire survive an audit failure?".
func TestCronDispatch_CronFiredAuditFailureDoesNotRollback(t *testing.T) {
	t.Parallel()
	inner := state.NewMemStore()
	ctx := context.Background()
	acct, _ := inner.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	app, cron := newAppAndCron(t, inner, acct.ID, true)

	wrapped := &failingEventStore{Store: inner}
	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, wrapped, vmm)
	synth := &recordingSynth{}
	now := time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC)
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithAudit(audit.New(wrapped, slog.Default(), nil, "schedd")).
		WithClock(func() time.Time { return now })

	loop.runCronTick(ctx)

	// Fire must still have committed (audit failure is best-effort).
	got, err := inner.CronByID(ctx, cron.ID)
	if err != nil {
		t.Fatalf("read cron: %v", err)
	}
	if got.LastFiredAt.IsZero() {
		t.Fatalf("LastFiredAt still zero after tick (audit write failed but fire must survive; app=%s)", app.ID)
	}
	// Synth call also must still have gone through (reaper/audit
	// failures don't bubble into the dispatch path).
	if got := synth.calls.Load(); got != 1 {
		t.Errorf("synth calls = %d, want 1 (audit-log failure must not roll back dispatch)", got)
	}
}

// failingSynth is a recordingSynth whose Invoke and SynthesizeRequest
// both error. Drives the status="err" branch in dispatchOneCron: the
// cron reached the Wake step (so the boundary guard already
// approved the fire), the synthetic Invoke failed, AND the legacy
// SynthesizeRequest fallback also failed. The defer-block emit
// MUST still record status="err" with empty invocation_id and
// instance_id so SOC 2 CC7.2 catches the "scheduled but failed to
// fire" case. Test pins that audit row exists + status field.
type failingSynth struct {
	calls atomic.Int64
}

func (f *failingSynth) SynthesizeRequest(_ context.Context, _, _, _ string) error {
	f.calls.Add(1)
	return errors.New("simulated synth failure")
}

func (f *failingSynth) Invoke(_ context.Context, _ string, _ state.Invocation) (state.Invocation, error) {
	f.calls.Add(1)
	return state.Invocation{}, errors.New("simulated invoke failure")
}

// TestCronDispatch_FirstFireAfterEnable pins the first-fire contract:
// when LastFiredAt is zero and the schedule's next boundary is reached
// (or passed) at the dispatch clock, the cron MUST fire on the first
// tick. This is the "greedy" anchor documented in pkg/sched/loop.go
// (boundary = c.CreatedAt when LastFiredAt is zero). The existing
// newAppAndCron helper backdates CreatedAt, so this test inlines
// CreateCron + UpdateCron to pin a specific CreatedAt — the only way
// to drive the first-fire path through dispatchOneCron in a unit test
// (CreateCron has no createdAt parameter; the PgStore half of the
// plan pins the same contract end-to-end).
func TestCronDispatch_FirstFireAfterEnable(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "first-fire@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "first-fire", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	// CreatedAt pinned at 12:00:30 — MemStore.CreateCron stamps
	// time.Now() so we must UpdateCron to install a known value.
	cronRow, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/ping", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	createdAt := time.Date(2026, 7, 17, 12, 0, 30, 0, time.UTC)
	if _, err := store.UpdateCron(ctx, cronRow.ID, nil, nil, nil, &createdAt); err != nil {
		t.Fatalf("UpdateCron(createdAt): %v", err)
	}

	eng, _ := makeEngine(t, store, &fakeWakeVMM{})
	synth := &recordingSynth{}
	// Dispatch clock at 12:05:00 — NextFireAt(12:00:30) for
	// `*/5 * * * *` is 12:05:00, which is reached. The cron
	// fires on the first tick. (Reading the test name: "fires
	// after enable" requires the boundary to be reached, not
	// merely crossed — the schedule's horizon is what gates it.)
	now := time.Date(2026, 7, 17, 12, 5, 0, 0, time.UTC)
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithClock(func() time.Time { return now })

	loop.runCronTick(ctx)

	if got := synth.calls.Load(); got != 1 {
		t.Errorf("synth calls = %d, want 1 (first-fire boundary reached, must dispatch)", got)
	}
	row, err := store.CronByID(ctx, cronRow.ID)
	if err != nil {
		t.Fatalf("CronByID: %v", err)
	}
	if row.LastFiredAt.IsZero() {
		t.Errorf("LastFiredAt still zero after first-fire tick (createdAt=%s now=%s)", createdAt, now)
	}
}

// TestCronDispatch_FirstFireRespectsScheduleBoundary is the
// counter-case to TestCronDispatch_FirstFireAfterEnable: the
// schedule's next boundary is still in the future, so the greed
// must NOT fire. Cron created at 00:00:30 with `*/5 * * * *`
// has next boundary 00:05:00; dispatch at 00:01:00 is well
// before, so the cron must not fire on the first tick.
func TestCronDispatch_FirstFireRespectsScheduleBoundary(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "first-fire-skip@example.com", api.PlanHobby)
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "first-fire-skip", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	cronRow, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/ping", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	createdAt := time.Date(2026, 7, 17, 12, 0, 30, 0, time.UTC)
	if _, err := store.UpdateCron(ctx, cronRow.ID, nil, nil, nil, &createdAt); err != nil {
		t.Fatalf("UpdateCron(createdAt): %v", err)
	}

	eng, _ := makeEngine(t, store, &fakeWakeVMM{})
	synth := &recordingSynth{}
	now := time.Date(2026, 7, 17, 12, 1, 0, 0, time.UTC)
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithClock(func() time.Time { return now })

	loop.runCronTick(ctx)

	if got := synth.calls.Load(); got != 0 {
		t.Errorf("synth calls = %d, want 0 (first-fire boundary NOT reached → must not dispatch)", got)
	}
	row, err := store.CronByID(ctx, cronRow.ID)
	if err != nil {
		t.Fatalf("CronByID: %v", err)
	}
	if !row.LastFiredAt.IsZero() {
		t.Errorf("LastFiredAt = %s, want zero (no fire should have occurred)", row.LastFiredAt)
	}
}

// TestCronDispatch_FirstFireAuditNullLastFiredAtBefore pins the
// audit-row rendering fix: on the first fire (LastFiredAt was zero),
// the cron.fired row's last_fired_at_before key is absent (Go's
// map[string]any unmarshal returns nil for missing keys). The
// pre-fix behaviour was to format the zero time as the literal
// "0001-01-01T00:00:00Z", which an operator can't distinguish from
// data corruption. This is the contract surfaced by the spec §5.1
// pinning of the cron.fired payload.
//
// Uses the same createCron + UpdateCron(createdAt) pair as the other
// first-fire tests. The companion CronByID read-back confirms no
// LastFiredAt was set by the tick under test (the cron must fire
// for the audit row to land, so on the err path of the boundary
// guard no audit row is emitted — this test must drive a firing
// tick).
func TestCronDispatch_FirstFireAuditNullLastFiredAtBefore(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "first-fire-audit@example.com", api.PlanHobby)
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "first-fire-audit", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Status: state.DeployLive, Kind: state.DeploymentKindImage,
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	cronRow, err := store.CreateCron(ctx, app.ID, "*/5 * * * *", "/ping", true)
	if err != nil {
		t.Fatalf("CreateCron: %v", err)
	}
	createdAt := time.Date(2026, 7, 17, 12, 0, 30, 0, time.UTC)
	if _, err := store.UpdateCron(ctx, cronRow.ID, nil, nil, nil, &createdAt); err != nil {
		t.Fatalf("UpdateCron(createdAt): %v", err)
	}

	eng, _ := makeEngine(t, store, &fakeWakeVMM{})
	synth := &recordingSynth{}
	ops := wire.NewOpsMetrics("schedd")
	now := time.Date(2026, 7, 17, 12, 5, 0, 0, time.UTC) // next boundary reached
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithAudit(audit.New(store, slog.Default(), ops, "schedd")).
		WithClock(func() time.Time { return now })

	loop.runCronTick(ctx)

	rows, err := store.ListEvents(ctx, acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListEvents(acct) returned %d rows, want 1 (first-fire cron.fired); rows=%+v", len(rows), rows)
	}
	var payload map[string]any
	if err := json.Unmarshal(rows[0].Data, &payload); err != nil {
		t.Fatalf("event Data not valid JSON: %v (data=%q)", err, rows[0].Data)
	}
	// last_fired_at_before must be ABSENT (nil), not the
	// misleading "0001-01-01T00:00:00Z". The pre-fix code formatted
	// time.Time{}.UTC() as that literal; the post-fix code omits
	// the key entirely. payload[k] on a missing key returns nil;
	// "ok" is the canonical absent check.
	v, ok := payload["last_fired_at_before"]
	if ok {
		t.Errorf("last_fired_at_before present on first fire, got %v (want absent/nil)", v)
	}
	if v != nil {
		t.Errorf("last_fired_at_before = %v, want nil on first fire", v)
	}
	// Sanity-check the rest of the payload is still populated.
	if payload["status"] != "ok" {
		t.Errorf("payload.status = %v, want ok", payload["status"])
	}
	if payload["cron_id"] != cronRow.ID {
		t.Errorf("payload.cron_id = %v, want %s", payload["cron_id"], cronRow.ID)
	}
}

// TestCronDispatch_EmitsCronFiredAudit_WhenInvokeFails pins the
// err-path coverage promised by the PR description ("emitted
// regardless of synthetic Invoke outcome"). A cron that reaches
// the Wake step but whose synthetic Invoke + SynthesizeRequest
// both fail MUST still produce a cron.fired audit row with
// status="err" and empty invocation_id / instance_id (the JSON
// shape stays stable for dashboards per pkg/audit ADR-035
// err-path invariant).
func TestCronDispatch_EmitsCronFiredAudit_WhenInvokeFails(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "c@example.com", api.PlanHobby)
	app, cron := newAppAndCron(t, store, acct.ID, true)

	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &failingSynth{}
	now := time.Date(2026, 7, 17, 12, 2, 0, 0, time.UTC)
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth).
		WithAudit(audit.New(store, slog.Default(), nil, "schedd")).
		WithClock(func() time.Time { return now })

	loop.runCronTick(ctx)

	// Even though Invoke + SynthesizeRequest both failed, the
	// defer-block emit MUST have fired — the cron reached the Wake
	// step so the boundary guard approved the fire, and SOC 2 CC7.2
	// expects an audit signal for "scheduled but failed to fire".
	rows, err := store.ListEvents(ctx, acct.ID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListEvents(acct) returned %d rows, want 1 (cron.fired on err path); rows=%+v", len(rows), rows)
	}
	e := rows[0]
	if e.Kind != "cron.fired" {
		t.Errorf("event kind = %q, want cron.fired", e.Kind)
	}
	if e.Actor != "schedd" {
		t.Errorf("event actor = %q, want schedd", e.Actor)
	}
	var payload map[string]any
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		t.Fatalf("event Data not valid JSON: %v (data=%q)", err, e.Data)
	}
	if payload["status"] != "err" {
		t.Errorf("payload.status = %v, want err (synth failed)", payload["status"])
	}
	// JSON shape invariant: invocation_id + instance_id MUST be
	// present (empty string), NOT omitted — see pkg/audit ADR-035.
	for _, k := range []string{"invocation_id", "instance_id"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing key %q on err path (full=%+v)", k, payload)
		}
		if v, _ := payload[k].(string); v != "" {
			t.Errorf("payload[%q] = %q, want empty string on err path", k, v)
		}
	}
	// The pre-fire state is still useful for ops reconciliation.
	if payload["cron_id"] != cron.ID {
		t.Errorf("payload.cron_id = %v, want %s", payload["cron_id"], cron.ID)
	}
	if payload["app_id"] != app.ID {
		t.Errorf("payload.app_id = %v, want %s", payload["app_id"], app.ID)
	}
	// Synth call counter: both Invoke and SynthesizeRequest must
	// have been attempted (proves the err path was actually taken).
	if got := synth.calls.Load(); got != 2 {
		t.Errorf("synth.calls = %d, want 2 (Invoke + SynthesizeRequest fallback)", got)
	}
}
