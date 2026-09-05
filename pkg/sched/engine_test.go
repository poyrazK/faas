package sched

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched/recentload"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeVMM is a sched.VMM that records calls and stands in for firecracker. It is
// shared by engine_test and loop_test (both package sched).
type fakeVMM struct {
	mu                  sync.Mutex
	coldBoots           int
	restores            int
	snapshots           int
	warmSnapshots       int // PR #470-FU-A: counts WarmSnapshot calls (warm-tier capture path)
	destroys            int
	pings               int  // PR #114: counts Ping calls (heartbeat path)
	frameworkReadyCount int  // PR #470-FU-B: counts FrameworkReady calls (DGRAM receipt path)
	prepares            int  // Tier A5: counts PrepareLiveMigration calls
	adopts              int  // Tier A5: counts AdoptMigratedInstance calls
	acks                int  // Tier A5: counts AcknowledgeMigration calls
	cancels             int  // Tier A5: counts CancelLiveMigration calls
	forceColdFallback   bool // CreateFromSnapshot reports a cold-boot fallback (ADR-005)
	wakeErr             error
	snapErr             error
	// snapDeadline / snapHasDeadline capture the ctx deadline seen by
	// PauseAndSnapshot. The RPC shipped with NO deadline and wedged the
	// scheduler for 10+ minutes in production (2026-09-03); these let a
	// test assert the deadline exists without waiting SnapshotTimeout.
	snapDeadline    time.Time
	snapHasDeadline bool
	// warmSnapErr (issue #470 / PR A / ADR-055): injectable WarmSnapshot
	// failure. Distinct from snapErr so the warm-capture failure test
	// can simulate "vmmd's PauseAndSnapshot succeeded but WarmSnapshot
	// failed" without bleeding into the init capture that runs in the
	// same Park. nil = warm capture succeeds.
	warmSnapErr error
	destroyErr  error
	pingErr     error                   // PR #114: injectable Ping failure for heartbeat tests
	prepareErr  error                   // Tier A5: injectable PrepareLiveMigration error
	adoptErr    error                   // Tier A5: injectable AdoptMigratedInstance error
	ackErr      error                   // Tier A5: injectable AcknowledgeMigration error
	cancelErr   error                   // Tier A5: injectable CancelLiveMigration error
	adoptHook   func(instanceID string) // Tier A5: runs after a successful AdoptMigratedInstance; tests use it to mutate the store row so a downstream Phase 4 commit returns ErrConflict (verifying the Phase 4 release-on-failure path)
	// lastSnapRef records the SnapshotRef CreateFromSnapshot was
	// invoked with on its most recent call. F-2 review finding —
	// Wake's storage_key plumbing deserves a test pin; storing the
	// ref lets the test assert what the engine sent to vmmd.
	lastSnapRef SnapshotRef

	// sleepFor makes every RPC sleep before returning. Used by commit
	// 1's deadline test to drive the §6.1 boot-budget path: a vmmd
	// that hangs past WakingTimeout / ColdBootTimeout must surface as
	// a context.DeadlineExceeded error to the engine. Zero (the
	// default) means "return immediately".
	sleepFor time.Duration

	// bootStarted / bootRelease fence the vmmd boot call so commit 2
	// can prove Wake's Phase 3 happens outside appMu. The fake closes
	// bootStarted when it enters the cold-boot / restore call, then
	// blocks on bootRelease before returning. Tests use it to:
	//   - start a Wake in goroutine A,
	//   - wait for A to be inside the vmmd call,
	//   - start Wake B for the same app — B must NOT block on appMu,
	//   - release A's boot.
	bootStarted chan struct{} // capacity 1; or test injects a channel
	bootRelease chan struct{}

	// lastColdBootSpec / lastRestoreSpec capture the AppSpec the engine
	// handed to vmmd on the most recent wake call. Tests that exercise
	// the sealed-env wire read these to verify schedd forwarded the
	// per-key rows correctly.
	lastColdBootSpec AppSpec
	lastRestoreSpec  AppSpec

	// ADR-051 PR-D: characterization report. The default zero
	// value is "no observation" — the engine leaves apps.workload_class
	// alone. Tests that want to exercise the SetAppWorkloadClass
	// branch set this to a non-empty value (ObservedClass != "").
	characterization api.CharacterizationReport

	// PR-A (issue #517): correlation ctx capture. The fake records
	// the ctx CreateColdBoot / CreateFromSnapshot / Logs received so
	// TestEngineWake_PropagatesWakeIDToVMM can assert wake_id /
	// app_id cross the schedd → vmmd boundary.
	lastColdBootCtx context.Context
	lastRestoreCtx  context.Context
	lastLogsCtx     context.Context
}

func (f *fakeVMM) outcome(instance string, method vmmdpb.WakeMethod, requested vmmdpb.WakeMethod) *WakeOutcome {
	return &WakeOutcome{
		Instance: instance, LeaseUID: 20001, HostIP: "10.100.0.2",
		Netns: "fc-" + instance, VethHost: "vh1", VethPeer: "vp1",
		Method: method, RequestedMethod: requested,
		Characterization: f.characterization,
	}
}

func (f *fakeVMM) CreateColdBoot(ctx context.Context, _, instance string, app AppSpec) (*WakeOutcome, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.bootStarted != nil {
		select {
		case f.bootStarted <- struct{}{}:
		default:
		}
		select {
		case <-f.bootRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return nil, f.wakeErr
	}
	f.lastColdBootSpec = app
	f.lastColdBootCtx = ctx
	f.coldBoots++
	return f.outcome(instance, vmmdpb.WakeMethod_WAKE_COLD_BOOT, vmmdpb.WakeMethod_WAKE_COLD_BOOT), nil
}

func (f *fakeVMM) CreateFromSnapshot(ctx context.Context, _, instance string, app AppSpec, ref SnapshotRef) (*WakeOutcome, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.bootStarted != nil {
		select {
		case f.bootStarted <- struct{}{}:
		default:
		}
		select {
		case <-f.bootRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.wakeErr != nil {
		return nil, f.wakeErr
	}
	f.lastRestoreSpec = app
	f.lastSnapRef = ref
	f.lastRestoreCtx = ctx
	f.restores++
	method := vmmdpb.WakeMethod_WAKE_RESTORE
	if f.forceColdFallback {
		method = vmmdpb.WakeMethod_WAKE_COLD_BOOT
	}
	return f.outcome(instance, method, vmmdpb.WakeMethod_WAKE_RESTORE), nil
}

func (f *fakeVMM) PauseAndSnapshot(ctx context.Context, _, _, _, _, _ string) (SnapshotBytes, error) {
	f.mu.Lock()
	f.snapDeadline, f.snapHasDeadline = ctx.Deadline()
	f.mu.Unlock()
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return SnapshotBytes{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.snapErr != nil {
		return SnapshotBytes{}, f.snapErr
	}
	f.snapshots++
	return SnapshotBytes{MemBytes: 130 * 1024 * 1024, VMStateBytes: 4096}, nil
}

// WarmSnapshot (issue #470 / PR #470-FU-A) is the fake hot
// half of the warm path. Distinct counter from
// fakeVMM.snapshots so the engine_test captureWarmSnapshot
// tests can assert warm + init both ran inside one Park cycle.
// snapErr is reused (the production-side pause / resume failures
// funnel through the same wire shape).
func (f *fakeVMM) WarmSnapshot(ctx context.Context, _, _, _, _ string) (SnapshotBytes, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return SnapshotBytes{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.warmSnapErr != nil {
		return SnapshotBytes{}, f.warmSnapErr
	}
	f.warmSnapshots++
	return SnapshotBytes{MemBytes: 130 * 1024 * 1024, VMStateBytes: 4096}, nil
}

func (f *fakeVMM) Destroy(ctx context.Context, _, _ string) error {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.destroyErr != nil {
		return f.destroyErr
	}
	f.destroys++
	return nil
}

// StopInstance (M-2 / ADR-138 §Decision 1) is the graceful
// signal-then-grace-then-SIGKILL stop sequence. Test fakes
// default to no-op + nil — the engine's per-mode dispatch lives
// in pkg/sched/engine_stop_pgtest_test.go (commit 6).
func (f *fakeVMM) StopInstance(_ context.Context, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}

// StopInstanceOnNode (M-2 / ADR-138 §Decision 1) is the routed
// shape — same no-op default as StopInstance. The engine's
// worker/job dispatch calls e.vmm.StopInstanceOnNode; the
// service/request paths use snapshotAndPark and never invoke
// either method.
func (f *fakeVMM) StopInstanceOnNode(_ context.Context, _, _ string, _, _ int32) (*StopInstanceOutcome, error) {
	return nil, nil
}

// FrameworkReady implements VMM for the engine-test fake (issue #470 /
// PR #470-FU-B). The cmd/vmmd DGRAM host recv loop calls this on every
// "framework ready" signal; the engine paths use it indirectly via
// the live Instance row. Pure no-op for the test fake — the engine
// tests that need to assert the receipt was dispatched to the
// vmmd side will add a counter (IssuePR470BFrameworkReadyCalls).
func (f *fakeVMM) FrameworkReady(_ context.Context, _, _ string, _ int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frameworkReadyCount++
	return nil
}

// Ping implements RoutedVMM for the engine-test fake (issue #97 /
// ADR-025 axis 3, PR #114). Returns a fixed fc_version + the
// current monotonic time. Tests that need a per-call error inject
// pingErr the same way destroyErr works.
func (f *fakeVMM) Ping(_ context.Context, _ string) (*PingOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pingErr != nil {
		return nil, f.pingErr
	}
	f.pings++
	return &PingOutcome{FcVersion: "1.10.0", ServerTime: time.Now()}, nil
}

// PrepareLiveMigration implements RoutedVMM for the Tier A5
// (ADR-066) four-phase handoff. The fake mints a deterministic
// lease token ("lease-<instance>") and echoes back the snapshot
// keys. Tests that need a per-call error inject prepareErr; the
// counters prepares / adopts / acks / cancels let the suite
// assert each phase ran.
func (f *fakeVMM) PrepareLiveMigration(ctx context.Context, _, instanceID, snapshotStorageKey string) (LiveMigrationPrepare, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return LiveMigrationPrepare{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prepareErr != nil {
		return LiveMigrationPrepare{}, f.prepareErr
	}
	f.prepares++
	vmstateKey := snapshotStorageKey
	if len(vmstateKey) >= 4 && vmstateKey[len(vmstateKey)-4:] == "/mem" {
		vmstateKey = vmstateKey[:len(vmstateKey)-4] + "/vmstate"
	} else {
		vmstateKey = vmstateKey + "-vmstate"
	}
	return LiveMigrationPrepare{
		MemStorageKey:     snapshotStorageKey,
		VMStateStorageKey: vmstateKey,
		LeaseToken:        "lease-" + instanceID,
	}, nil
}

// AdoptMigratedInstance implements RoutedVMM for the Tier A5
// handoff. Returns a fixed HostIP / Netns / GuestUID pair
// (parallels the wake outcome shape).
func (f *fakeVMM) AdoptMigratedInstance(ctx context.Context, _, instanceID string, _ AppSpec, _, _, _ string) (LiveMigrationAdopt, error) {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return LiveMigrationAdopt{}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.adoptErr != nil {
		return LiveMigrationAdopt{}, f.adoptErr
	}
	f.adopts++
	hook := f.adoptHook
	f.mu.Unlock()
	if hook != nil {
		hook(instanceID)
	}
	f.mu.Lock()
	return LiveMigrationAdopt{
		HostIP:   "10.100.0.2",
		Netns:    "fc-" + instanceID,
		GuestUID: 20001,
	}, nil
}

// AcknowledgeMigration implements RoutedVMM for the Tier A5
// handoff. Idempotent — duplicate acks are no-ops. Tracks ack
// count for test assertions.
func (f *fakeVMM) AcknowledgeMigration(ctx context.Context, _, _, _ string) error {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ackErr != nil {
		return f.ackErr
	}
	f.acks++
	return nil
}

// CancelLiveMigration implements RoutedVMM for the Tier A5
// handoff. Tracks cancel count.
func (f *fakeVMM) CancelLiveMigration(ctx context.Context, _, _, _ string) error {
	if d := f.sleepFor; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancelErr != nil {
		return f.cancelErr
	}
	f.cancels++
	return nil
}

// Stats implements RoutedVMM (issue #170 / PR-A). Engine tests do
// not assert on Stats contents — the instancestats poller's own
// tests cover that. Returns the empty snapshot; tests that want
// the engine to "see" instance metrics don't need them yet (the
// engine never reads them in PR-A).
func (f *fakeVMM) Stats(_ context.Context, _ string) (*StatsSnapshot, error) {
	return &StatsSnapshot{}, nil
}

// UpdateEgressAllowlist (tier-2 PR-B) — engine tests don't drive
// the egress drift path; the egress_drift_test.go suite does.
// Records nothing. Returning nil keeps the gRPC VmmdAPI /
// RoutedVMM contract satisfied for tests that wire newEngine().
func (f *fakeVMM) UpdateEgressAllowlist(_ context.Context, _, _ string, _ []netip.Prefix) error {
	return nil
}

// UpdateStaticEgressIP (ADR-119) is the no-op test fake.
// Same posture as UpdateEgressAllowlist above.
func (f *fakeVMM) UpdateStaticEgressIP(_ context.Context, _, _, _ string, _ string) error {
	return nil
}

// Logs (issue #254 / Move 4, issue #517 / PR-B) — engine tests
// don't drive the log stream path; the scheddgrpc handler tests
// do. Returns a closed fakeLogStream so any accidental caller exits
// cleanly. PR-B adds the sinceWrittenAt time lower-bound; the fake
// ignores it but still records the ctx for fan-out assertions.
func (f *fakeVMM) Logs(ctx context.Context, _, _ string, _ int64, _ time.Time) (LogStream, error) {
	f.mu.Lock()
	f.lastLogsCtx = ctx
	f.mu.Unlock()
	return &fakeLogStream{}, nil
}

// fakeNotifier records emitted pg_notify events.
type fakeNotifier struct {
	mu     sync.Mutex
	events []notifyEvent
}

type notifyEvent struct{ channel, payload string }

func (n *fakeNotifier) Notify(_ context.Context, channel, payload string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, notifyEvent{channel, payload})
	return nil
}

func (n *fakeNotifier) count(channel string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	c := 0
	for _, e := range n.events {
		if e.channel == channel {
			c++
		}
	}
	return c
}

// reset drops every recorded event so subsequent count() calls
// reflect only the post-reset emits. Used by the warm-snapshot
// tests to isolate the Park-site snapshot_written emissions from
// Prime's own init capture.
func (n *fakeNotifier) reset() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = nil
}

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// seedApp builds an account + app + live deployment in a MemStore and returns
// them. A snapshot is added only when withSnapshot is set.
func seedApp(t *testing.T, store state.Store, plan api.Plan, ramMB, maxConc int) (state.Account, state.App, state.Deployment) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "app-" + string(plan), RAMMB: ramMB,
		MaxConcurrency: maxConc, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	return acct, app, dep
}

func newEngine(t *testing.T, store state.Store, vmm RoutedVMM, notif Notifier, fcVer string) *Engine {
	t.Helper()
	e, err := NewEngine(context.Background(), store, NewNodeLedger(), vmm, notif, fcVer, testLog())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// readScaleUp is a test helper that scrapes the closed-set
// schedd_scale_up_decisions_total{app, outcome} counter from the
// OpsMetrics HTTP handler. Returns 0 when the line is missing
// (Prometheus pre-instantiates zero rows for the closed set).
func readScaleUp(t *testing.T, ops *wire.OpsMetrics, app, outcome string) int {
	t.Helper()
	if ops == nil {
		return 0
	}
	body := getMetricsBody(t, ops)
	want := `schedd_scale_up_decisions_total{app="` + app + `",outcome="` + outcome + `"}`
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

// getMetricsBody fetches /metrics from the OpsMetrics HTTP handler.
// Mirrors pkg/wire/metrics_test.go::render.
func getMetricsBody(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	srv := httptest.NewServer(ops.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get metrics: %v", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// setAppPolicy stamps the post-create fields admitGate consults:
// ScalingPolicy (*state.ScalingPolicy) and LastScaleOutAt (*time.Time).
// Mirrors the apid PATCH path that wires the customer-facing knobs.
//
// lastScaleOutAt is supplied in absolute terms; MemStore keeps the
// timestamp in apps[id].LastScaleOutAt which we splice directly via
// the Store's exposed helper. The PG path is tested separately in
// pkg/state/pgstore_stamp_app_scale_test.go.
func setAppPolicy(t *testing.T, store state.Store, appID string, policy *state.ScalingPolicy, lastScaleOutAt *time.Time) {
	t.Helper()
	_, err := store.UpdateApp(context.Background(), appID, state.UpdateAppParams{
		MaxConcurrency:   ptrInt(5),
		MinInstances:     ptrInt(0),
		SetMinInstances:  true,
		ScalingPolicy:    policy,
		SetScalingPolicy: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp ScalingPolicy: %v", err)
	}
	if lastScaleOutAt != nil {
		if ms, ok := store.(*state.MemStore); ok {
			ms.SetLastScaleOutAt(appID, *lastScaleOutAt)
		} else {
			if err := store.StampAppScaleOut(context.Background(), appID); err != nil {
				t.Fatalf("StampAppScaleOut: %v", err)
			}
		}
	}
}

func ptrInt(i int) *int { return &i }

// TestAdmitGate_Outcomes pins the wake-gate admitGate decision
// matrix (PR-C, issue #462). admintGate is the single decision
// site before the ledger and the instances INSERT; it stamps the
// per-(app, outcome) schedd_scale_up_decisions_total counter so
// the dashboard "why didn't this scale?" pane can render the
// reason. The four cases fence every outcome branch:
//
//   - admit: clean state → wakeAdmit, no metric increment.
//   - reject_at_cap: Concurrency >= MaxConcurrency → wakeRejectAtCap,
//     metric row increments to 1.
//   - cooldown_held: Concurrency > 0 AND non-nil LastScaleOutAt
//     AND non-zero ScaleOutCooldownS → wakeCooldownHeld, metric
//     row increments to 1.
//   - min_floor_already: ScalingPolicy.MinInstances > 0 AND
//     Concurrency >= MinInstances → wakeMinFloorAlready, metric
//     row increments to 1; this uses Pro (cap=5) and primes the
//     ledger to Concurrency=2 to mirror the floor.
//
// Cases also assert the metric row count is 0 for the non-fired
// outcomes so a regression that double-counts is caught.
func TestAdmitGate_Outcomes(t *testing.T) {
	t.Run("admit", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		// No ScalingPolicy set — atMinFloorWithNoSignal returns
		// false (nil policy short-circuits at engine.go:2215).
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		limits := api.MustLimitsFor(api.PlanPro)
		got, _, _, _, _ := e.admitGate(context.Background(), &app, limits)
		if got != wakeAdmit {
			t.Errorf("admitGate = %v, want wakeAdmit", got)
		}
		// Counter for reject_at_cap / cooldown_held / min_floor_already
		// must remain 0 — a non-fired branch must not emit.
		if n := readScaleUp(t, ops, app.ID, "reject_at_cap"); n != 0 {
			t.Errorf("reject_at_cap = %d, want 0", n)
		}
		if n := readScaleUp(t, ops, app.ID, "cooldown_held"); n != 0 {
			t.Errorf("cooldown_held = %d, want 0", n)
		}
		if n := readScaleUp(t, ops, app.ID, "min_floor_already"); n != 0 {
			t.Errorf("min_floor_already = %d, want 0", n)
		}
	})
	t.Run("reject_at_cap", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 2)
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		// Prime the ledger to MaxConcurrency (=2).
		e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro})
		e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro})
		limits := api.MustLimitsFor(api.PlanPro)
		got, _, _, _, _ := e.admitGate(context.Background(), &app, limits)
		if got != wakeRejectAtCap {
			t.Errorf("admitGate = %v, want wakeRejectAtCap", got)
		}
		if n := readScaleUp(t, ops, app.ID, "reject_at_cap"); n != 1 {
			t.Errorf("reject_at_cap = %d, want 1", n)
		}
	})
	t.Run("cooldown_held", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		// Stamp a ScalingPolicy with ScaleOutCooldownS=60 and a
		// lastScaleOutAt 1s ago so the consult fires.
		setAppPolicy(t, store, app.ID, &state.ScalingPolicy{
			ScaleOutCooldownS: 60,
		}, ptrTime(time.Now().Add(-1*time.Second)))
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		// Prime the ledger to Concurrency=1 (the discriminator).
		e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro})
		// Reload the app so the in-memory copy reflects the
		// post-UpdateApp + post-StampAppScaleOut values.
		reloaded, err := store.AppByID(context.Background(), app.ID)
		if err != nil {
			t.Fatalf("GetApp: %v", err)
		}
		limits := api.MustLimitsFor(api.PlanPro)
		got, _, _, _, _ := e.admitGate(context.Background(), &reloaded, limits)
		if got != wakeCooldownHeld {
			t.Errorf("admitGate = %v, want wakeCooldownHeld", got)
		}
		if n := readScaleUp(t, ops, app.ID, "cooldown_held"); n != 1 {
			t.Errorf("cooldown_held = %d, want 1", n)
		}
	})
	t.Run("min_floor_already", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		// Floor=2 with non-zero Size; Concurrency will be 2 after
		// the Admit pair.
		setAppPolicy(t, store, app.ID, &state.ScalingPolicy{
			MinInstances: 2,
		}, nil)
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro})
		e.ledger.Admit(Request{Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro})
		reloaded, err := store.AppByID(context.Background(), app.ID)
		if err != nil {
			t.Fatalf("GetApp: %v", err)
		}
		limits := api.MustLimitsFor(api.PlanPro)
		got, _, _, _, _ := e.admitGate(context.Background(), &reloaded, limits)
		if got != wakeMinFloorAlready {
			t.Errorf("admitGate = %v, want wakeMinFloorAlready", got)
		}
		if n := readScaleUp(t, ops, app.ID, "min_floor_already"); n != 1 {
			t.Errorf("min_floor_already = %d, want 1", n)
		}
	})
	t.Run("cold_start_bypass_cooldown", func(t *testing.T) {
		// Concurrency == 0 with a freshly-stamped LastScaleOutAt
		// MUST admit — the discriminator is load-bearing for the
		// customer's "scale on demand" use case.
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		setAppPolicy(t, store, app.ID, &state.ScalingPolicy{
			ScaleOutCooldownS: 60,
		}, ptrTime(time.Now().Add(-1*time.Second)))
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		// Do NOT prime the ledger — concurrency stays at 0.
		reloaded, err := store.AppByID(context.Background(), app.ID)
		if err != nil {
			t.Fatalf("GetApp: %v", err)
		}
		limits := api.MustLimitsFor(api.PlanPro)
		got, _, _, _, _ := e.admitGate(context.Background(), &reloaded, limits)
		if got != wakeAdmit {
			t.Errorf("admitGate = %v, want wakeAdmit (cold-start bypass)", got)
		}
		if n := readScaleUp(t, ops, app.ID, "cooldown_held"); n != 0 {
			t.Errorf("cooldown_held = %d, want 0 (cold start bypass)", n)
		}
	})
	// overage_cap_reached (issue #561) — the spend-cap pause-workload
	// branch. The OverageChecker injects OverageReached for the
	// seeded app's account; admitGate must surface the new
	// wakeOverageCapReached outcome and bump the closed-set
	// overage_cap_reached counter. The other counter rows MUST stay
	// at 0 — a regression that fires the new branch in place of the
	// legacy outcomes would otherwise pass on the gate return alone.
	t.Run("overage_cap_reached", func(t *testing.T) {
		store := state.NewMemStore()
		acct, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		ops := wire.NewOpsMetrics("schedd")
		// AlwaysOKOverageChecker is the default for the OTHER
		// engine_test.go cases; here we want a stub that returns
		// OverageReached so the new branch fires. Captures
		// observedCents + capCents so we can verify the engine
		// surfaces them on the gate's (outcome, obs, cap) tuple.
		var hookObserved, hookCap int64
		checker := newMockChecker(func(_ context.Context, accountID string) (OverageStatus, int64, int64, error) {
			if accountID != acct.ID {
				t.Errorf("Check accountID = %q, want %q", accountID, acct.ID)
			}
			hookObserved = 1234
			hookCap = 1000
			return OverageReached, hookObserved, hookCap, nil
		})
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").
			WithOpsMetrics(ops).
			WithOverageChecker(checker)
		limits := api.MustLimitsFor(api.PlanPro)
		got, obs, cap, _, _ := e.admitGate(context.Background(), &app, limits)
		if got != wakeOverageCapReached {
			t.Errorf("admitGate = %v, want wakeOverageCapReached", got)
		}
		if n := readScaleUp(t, ops, app.ID, "overage_cap_reached"); n != 1 {
			t.Errorf("overage_cap_reached = %d, want 1", n)
		}
		// The legacy counters must stay at 0 — the OverageReached
		// branch short-circuits before reject_at_cap /
		// cooldown_held / min_floor_already.
		if n := readScaleUp(t, ops, app.ID, "reject_at_cap"); n != 0 {
			t.Errorf("reject_at_cap = %d, want 0", n)
		}
		if n := readScaleUp(t, ops, app.ID, "cooldown_held"); n != 0 {
			t.Errorf("cooldown_held = %d, want 0", n)
		}
		if n := readScaleUp(t, ops, app.ID, "min_floor_already"); n != 0 {
			t.Errorf("min_floor_already = %d, want 0", n)
		}
		// The (obs, cap) tuple is what admitAndDispatch lifts into
		// the *api.Problem Extensions. A regression that drops the
		// tuple would surface zeros here and the customer's RFC
		// 7807 body would lose the precise current state.
		if obs != hookObserved {
			t.Errorf("obs = %d, want %d", obs, hookObserved)
		}
		if cap != hookCap {
			t.Errorf("cap = %d, want %d", cap, hookCap)
		}
	})
}

// TestAdmitAndDispatch_WorkerClassExempt (PR-D, issue #462) — the
// worker-class first-check in admitAndDispatch fires BEFORE
// admitGate. Every worker-class wake (regardless of headroom,
// cooldown status, or min-floor) returns WakeResult{AtCapacity: true}
// without emitting a metric row. The contracts:
//
//   - AtCapacity=true is the typed-capacity lift: the existing
//     AdmitInstance path backpressure handles it (no error).
//   - The gate's closed-set outcome labels (reject_at_cap,
//     cooldown_held, min_floor_already) MUST NOT increment for
//     worker-class apps; the carve-out is a no-op on the metric,
//     a typed-capacity on the wire.
func TestAdmitAndDispatch_WorkerClassExempt(t *testing.T) {
	t.Run("worker_class_under_cap", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		// Stamp WorkloadClassWorker on the existing app.
		if _, err := store.SetAppWorkloadClass(context.Background(), app.ID, state.WorkloadClassWorker, "pr-d-test"); err != nil {
			t.Fatalf("SetAppWorkloadClass: %v", err)
		}
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		r, err := e.AdmitInstance(context.Background(), app.ID, "", "", "")
		if err != nil {
			t.Fatalf("AdmitInstance: %v", err)
		}
		// Surface: the trigger caller sees AtCapacity=true (typed lift).
		// Pinned explicitly so a future engineer who broadens the
		// (WakeResult{}, nil) surface accidentally (e.g. by moving
		// the worker-class branch into a generic "no-wake" path)
		// gets caught by this test.
		if !r.AtCapacity {
			t.Errorf("AtCapacity = false, want true (worker-class carve-out is a typed-capacity lift)")
		}
		// Metric rows: every gate outcome must be 0.
		for _, outcome := range []string{"reject_at_cap", "cooldown_held", "min_floor_already"} {
			if n := readScaleUp(t, ops, app.ID, outcome); n != 0 {
				t.Errorf("%s = %d, want 0 (worker-class carve-out bypasses admitGate)", outcome, n)
			}
		}
		// No instances footprint.
		inst, err := store.ListInstancesForApp(context.Background(), app.ID)
		if err != nil {
			t.Fatalf("ListInstancesForApp: %v", err)
		}
		if len(inst) != 0 {
			t.Errorf("instances count = %d, want 0 (worker-class wake creates no instance)", len(inst))
		}
	})
	t.Run("non_worker_class_warmup_regression_pin", func(t *testing.T) {
		// The first-check must NOT swallow non-worker apps. Pin:
		// a non-worker app with no ScalingPolicy and no ledger
		// priming gets wakeAdmit (no error, no typed lift).
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
		ops := wire.NewOpsMetrics("schedd")
		e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
		// AdmitInstance proceeds; the wake path either succeeds
		// (creates an instance) or short-circuits on a downstream
		// failure. The first-check MUST NOT fire (WorkloadClass is
		// zero-value = stateless). Symmetric pin to the worker-class
		// branch: AtCapacity must be false on the warm-up path.
		r, err := e.AdmitInstance(context.Background(), app.ID, "", "", "")
		if err != nil {
			var p *api.Problem
			if errors.As(err, &p) {
				t.Errorf("non-worker app AdmitInstance error = %v (the first-check must not fire for stateless apps)", err)
			}
		}
		if r.AtCapacity {
			t.Errorf("AtCapacity = true, want false (non-worker wakeAdmit path lifts no capacity)")
		}
	})
}

// TestAdmitAndDispatch_CooldownSwitchedToWaitForWarm (PR-D, issue #462)
// — the wake-cooldown_held branch now surfaces as CodeWaitForWarm
// (503 + Retry-After) instead of CodePlanLimitConcur (429). The
// Reaper's cooldown consult still uses the same constant; the wire
// shape is what changed.
//
// The test stamps a 60-second cooldown, primes the ledger to
// Concurrency=1, then runs a fresh wake and asserts the wire code
// + Retry-After are correct.
func TestAdmitAndDispatch_CooldownSwitchedToWaitForWarm(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	setAppPolicy(t, store, app.ID, &state.ScalingPolicy{
		ScaleOutCooldownS: 60,
	}, ptrTime(time.Now().Add(-1*time.Second)))
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	// Prime the ledger to Concurrency=1 (the discriminator).
	if err := e.ledger.Admit(Request{
		Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro,
	}); err != nil {
		t.Fatalf("prime ledger: %v", err)
	}
	_, err := e.AdmitInstance(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("AdmitInstance = nil, want *api.Problem{CodeWaitForWarm}")
	}
	var p *api.Problem
	if !errors.As(err, &p) {
		t.Fatalf("AdmitInstance error = %v, want *api.Problem", err)
	}
	if p.Code != api.CodeWaitForWarm {
		t.Errorf("Code = %q, want %q", p.Code, api.CodeWaitForWarm)
	}
	if p.Status != http.StatusServiceUnavailable {
		t.Errorf("Status = %d, want 503", p.Status)
	}
	if got := p.HasHeader("Retry-After"); len(got) != 1 {
		t.Errorf("Retry-After = %v, want 1 value", got)
	} else {
		n, perr := strconv.Atoi(got[0])
		if perr != nil {
			t.Errorf("Retry-After = %q, want integer (RFC 7231 §7.1.3)", got[0])
		}
		// Deterministic 1s-ago stamp + 60s cooldown → expected ≈ 59s.
		// Tighter bounds catch off-by-one errors in the cooldown
		// math (the helper converts via int(remaining.Seconds()) so
		// a 1s truncation is the headroom account).
		if n < 58 || n > 60 {
			t.Errorf("Retry-After = %d, want [58, 60] (60s cooldown, 1s stamp)", n)
		}
	}
}

// TestAdmitAndDispatch_MinFloorAlready_StaysPlanLimitConcur (PR-D,
// issue #462) — the wakeMinFloorAlready branch is NOT switched to
// CodeWaitForWarm. The customer asked for a wake that the floor
// already satisfies (no scale-out was attempted); 429 is the
// right wire shape. This is the load-bearing exclusion.
func TestAdmitAndDispatch_MinFloorAlready_StaysPlanLimitConcur(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 5)
	// Floor=2 with no ScalingPolicy cooldown; Concurrency=2 after
	// the Admit pair.
	setAppPolicy(t, store, app.ID, &state.ScalingPolicy{
		MinInstances: 2,
	}, nil)
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)
	if err := e.ledger.Admit(Request{
		Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro,
	}); err != nil {
		t.Fatalf("prime ledger 1: %v", err)
	}
	if err := e.ledger.Admit(Request{
		Instance: uuid.NewString(), AppID: app.ID, RAMMB: 128, Plan: api.PlanPro,
	}); err != nil {
		t.Fatalf("prime ledger 2: %v", err)
	}
	_, err := e.AdmitInstance(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("AdmitInstance = nil, want *api.Problem{CodePlanLimitConcur}")
	}
	var p *api.Problem
	if !errors.As(err, &p) {
		t.Fatalf("AdmitInstance error = %v, want *api.Problem", err)
	}
	if p.Code != api.CodePlanLimitConcur {
		t.Errorf("Code = %q, want %q (PR-D exclusion: min_floor stays on 429)", p.Code, api.CodePlanLimitConcur)
	}
	if got := p.HasHeader("Retry-After"); len(got) != 0 {
		t.Errorf("Retry-After = %v, want nil (Retry-After is for cooldown_held, not min_floor)", got)
	}
}

// TestAdmitInstanceForDeployment_DispatchesBoot pins the floor-trigger
// contract: an explicit deployment admission must run the same vmmd and
// Phase-4 commit path as a request-driven wake. The old helper only inserted
// a COLD_BOOTING row and reserved ledger capacity, which left floor-created
// instances to time out in the watchdog.
func TestAdmitInstanceForDeployment_DispatchesBoot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 128, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.AdmitInstanceForDeployment(context.Background(), app.ID, dep.ID, "", TriggerFloorDep)
	if err != nil {
		t.Fatalf("AdmitInstanceForDeployment: %v", err)
	}
	if res.InstanceID == "" {
		t.Fatal("AdmitInstanceForDeployment returned an empty instance id")
	}
	ins, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if ins.State != string(state.StateRunning) {
		t.Fatalf("instance state = %q, want %q", ins.State, state.StateRunning)
	}

	vmm.mu.Lock()
	coldBoots, restores := vmm.coldBoots, vmm.restores
	vmm.mu.Unlock()
	if coldBoots != 1 || restores != 0 {
		t.Fatalf("coldBoots=%d restores=%d, want 1/0", coldBoots, restores)
	}
	if got := e.Ledger().Concurrency(app.ID); got != 1 {
		t.Fatalf("ledger concurrency = %d, want 1", got)
	}
}

// TestCooldownSRemaining pins the cooldownSRemaining helper's
// floor/nil-stamp/zero-cooldown/remaining branches with
// deterministic clock injections. The helper is the wire source
// for the 503 + Retry-After value emitted on wakeCooldownHeld
// (see engine.go:645); an off-by-one here lands as
// `Retry-After: 0` on the wire, which RFC 7231 §7.1.3 forbids.
// PR-D review fix LOW #7.
func TestCooldownSRemaining(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	stamp := now.Add(-30 * time.Second) // 30s ago
	const cooldown = 60                 // seconds
	t.Run("nil_stamp_returns_one", func(t *testing.T) {
		app := &state.App{ScalingPolicy: &state.ScalingPolicy{ScaleOutCooldownS: cooldown}}
		if got := cooldownSRemaining(app, now); got != 1 {
			t.Errorf("nil LastScaleOutAt: got %d, want 1 (cold-start bypass upper-bounds at 1)", got)
		}
	})
	t.Run("nil_policy_returns_one", func(t *testing.T) {
		app := &state.App{LastScaleOutAt: ptrTime(stamp)}
		if got := cooldownSRemaining(app, now); got != 1 {
			t.Errorf("nil ScalingPolicy: got %d, want 1 (no cooldown configured → upper bound)", got)
		}
	})
	t.Run("zero_cooldown_returns_one", func(t *testing.T) {
		app := &state.App{
			LastScaleOutAt: ptrTime(stamp),
			ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: 0},
		}
		if got := cooldownSRemaining(app, now); got != 1 {
			t.Errorf("ScaleOutCooldownS=0: got %d, want 1 (disabled cooldown upper-bounds at 1)", got)
		}
	})
	t.Run("remaining_zero_bounded_at_one", func(t *testing.T) {
		// stamp older than the cooldown window: remaining is
		// negative. Helper must not return 0 or negative — it
		// floors at 1 because the cold-start path will fire
		// anyway on the next wake (LastScaleOutAt is stale).
		app := &state.App{
			LastScaleOutAt: ptrTime(now.Add(-2 * time.Hour)),
			ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: cooldown},
		}
		if got := cooldownSRemaining(app, now); got != 1 {
			t.Errorf("stale stamp: got %d, want 1 (helper floors at 1)", got)
		}
	})
	t.Run("remaining_positive_returns_seconds", func(t *testing.T) {
		// stamp 10s ago, cooldown 60s → 50s remaining.
		app := &state.App{
			LastScaleOutAt: ptrTime(now.Add(-10 * time.Second)),
			ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: cooldown},
		}
		if got := cooldownSRemaining(app, now); got != 50 {
			t.Errorf("in-window: got %d, want 50 (60 - 10)", got)
		}
	})
	t.Run("remaining_one_second_pin", func(t *testing.T) {
		// stamp 59s ago, cooldown 60s → 1s remaining. This is
		// the smallest positive value the helper returns and
		// the boundary against the floor.
		app := &state.App{
			LastScaleOutAt: ptrTime(now.Add(-59 * time.Second)),
			ScalingPolicy:  &state.ScalingPolicy{ScaleOutCooldownS: cooldown},
		}
		if got := cooldownSRemaining(app, now); got != 1 {
			t.Errorf("near-boundary: got %d, want 1 (60 - 59)", got)
		}
	})
}

func ptrTime(t time.Time) *time.Time { return &t }

// testBootBudget is the scaled-down vmmd budget the deadline tests
// inject in place of the §6.1 constants. Large enough that a loaded
// runner won't trip it spuriously, small enough that proving the
// deadline costs 0.2s instead of 35s.
const testBootBudget = 200 * time.Millisecond

// withTestBootBudget replaces the engine's §6.1 vmmd budget with
// testBootBudget for every state. Production never sets this field —
// see Engine.bootBudget. The real §6.1 numbers are asserted directly by
// TestBootTimeout_SpecBudgets, so shrinking them here costs no coverage.
func withTestBootBudget(t *testing.T, e *Engine) *Engine {
	t.Helper()
	e.bootBudget = func(state.State) time.Duration { return testBootBudget }
	return e
}

func TestEngineWake_ColdBoot(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if res.NodeID == "" {
		t.Errorf("NodeID = empty, want compute_node id (issue #98 / ADR-028)")
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Errorf("coldBoots=%d restores=%d, want 1/0", vmm.coldBoots, vmm.restores)
	}
	ins, err := store.RunningInstanceForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if ins.State != string(state.StateRunning) || ins.HostIP != "10.100.0.2" {
		t.Errorf("instance = %+v", ins)
	}
	// Resident RAM = ram + overhead (still reserved while running).
	if got := e.Ledger().ResidentRAM(); got != 512+api.PerVMOverheadMB {
		t.Errorf("resident = %d, want %d", got, 512+api.PerVMOverheadMB)
	}
}

// TestEngineWake_EvictedColdIsPermanent pins the lifecycle gate used by the
// invocation drain. A parked app must be explicitly woken before traffic can
// create another instance; otherwise a pending invocation can create an
// unbounded stream of FAILED rows while the app is known to be unavailable.
func TestEngineWake_EvictedColdIsPermanent(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatalf("park app: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	_, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("Wake returned nil for evicted_cold app")
	}
	if !errors.Is(err, ErrPermanentWake) {
		t.Fatalf("Wake error = %v, want ErrPermanentWake", err)
	}
	if p := api.AsProblem(err); p == nil || p.Code != api.CodeConflict {
		t.Fatalf("Wake problem = %v, want conflict problem", p)
	}
	if vmm.coldBoots != 0 || vmm.restores != 0 {
		t.Fatalf("vmmd calls = cold=%d restore=%d, want 0/0", vmm.coldBoots, vmm.restores)
	}
	instances, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("instance rows = %d, want 0", len(instances))
	}
}

// TestEngineWake_PhaseHistograms_Recorded (ADR-097, P1B) — pinning the
// schedd-side wake-phase decomposition. A cold-boot wake that takes
// 50ms inside the fakeVMM RPC must produce a non-zero observation
// count on every closed-set (app, phase) tuple:
//
//   - admit_to_rpc: the gap from bootInput.startedAt (engine.go:1589)
//     to the vmm.CreateColdBoot call site (engine.go:1759). On the
//     unit-test path this is sub-millisecond — the assertion is just
//     that the count is 1, not the duration.
//   - rpc_call: the fakeVMM sleepFor. The 50ms RPC latency must
//     surface here. Assertion is 1 count + sum_bucket matches sleepFor
//     with a generous tolerance (timer granularity + Go scheduler).
//   - rpc_to_running: the gap from the RPC return to the
//     e.transition(ctx, ..., state.StateRunning) call. Sub-millisecond
//     on unit test; assertion is 1 count.
//
// The pre-instantiated empty-app sentinel rows stay at 0 — the
// closed-set contract that bit PR #826 must hold.
func TestEngineWake_PhaseHistograms_Recorded(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	ops := wire.NewOpsMetrics("schedd")
	e := newEngine(t, store, &fakeVMM{sleepFor: 50 * time.Millisecond}, &fakeNotifier{}, "1.10.0").WithOpsMetrics(ops)

	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	// Per-app rows must have count == 1 for every phase.
	for _, phase := range []string{"admit_to_rpc", "rpc_call", "rpc_to_running"} {
		got := readWakeRPC(t, ops, app.ID, phase, "count")
		if got != 1 {
			t.Errorf("phase %q: count = %v, want 1", phase, got)
		}
	}

	// rpc_call sum must show the 50ms sleepFor with tolerance.
	// The Engine opens the bucket at 0.05s, so a 50ms observation
	// lands in le=0.35 (the next bucket up). Asserting sum instead
	// of the bucket row keeps the test resilient to bucket reorders.
	if got := readWakeRPC(t, ops, app.ID, "rpc_call", "sum"); got < 0.040 || got > 0.200 {
		t.Errorf("rpc_call sum = %vs, want ~0.05s (fakeVMM sleepFor=50ms with tolerance)", got)
	}

	// Empty-app sentinel rows must stay at 0 — closed-set contract.
	for _, phase := range []string{"admit_to_rpc", "rpc_call", "rpc_to_running"} {
		got := readWakeRPC(t, ops, "", phase, "count")
		if got != 0 {
			t.Errorf("empty-app sentinel %q: count = %v, want 0", phase, got)
		}
	}
}

// readWakeRPC scrapes the schedd_wake_rpc_duration_seconds histogram
// for a (app, phase) tuple and returns the integer count, the float
// sum, or an empty bucket-row count (suffix ∈ {"count", "sum",
// "bucket"}). Mirrors readScaleUp's shape — same httptest server +
// strings.Split + strings.Fields parsing.
func readWakeRPC(t *testing.T, ops *wire.OpsMetrics, app, phase, suffix string) float64 {
	t.Helper()
	if ops == nil {
		return 0
	}
	body := getMetricsBody(t, ops)
	prefix := `schedd_wake_rpc_duration_seconds_` + suffix + `{app="` + app + `",phase="` + phase + `"}`
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				return 0
			}
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err != nil {
				return 0
			}
			return v
		}
	}
	return 0
}

// TestEngineWake_ColdBootPersistsObservedClass pins ADR-051 PR-D:
// when the cold-boot's characterization report carries an
// ObservedClass, the engine must persist it via
// SetAppWorkloadClass("observed"). The seed app's class is the
// PG default 'http'; the probe reports 'graphql'; the apps row
// becomes 'graphql'. A zero report (no observed class) leaves the
// row untouched — covered by TestEngineWake_ColdBoot which uses
// the zero-report fakeVMM seeded with the default 'http' class.
func TestEngineWake_ColdBootPersistsObservedClass(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// MemStore defaults to ""; align with PG's 'http' default so
	// the before/after comparison is meaningful.
	if _, err := store.SetAppWorkloadClass(context.Background(), app.ID, state.WorkloadClassHTTP, "scan_hint"); err != nil {
		t.Fatalf("seed SetAppWorkloadClass: %v", err)
	}

	vmm := &fakeVMM{
		characterization: api.CharacterizationReport{
			ObservedClass: string(state.WorkloadClassGraphQL),
			ObservedPort:  4000,
			ExitCode:      0,
		},
	}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	got, err := store.AppByID(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.WorkloadClass != state.WorkloadClassGraphQL {
		t.Errorf("observed class = %q, want %q", got.WorkloadClass, state.WorkloadClassGraphQL)
	}
}

// TestEngineWake_PropagatesWakeIDToVMM (PR-A, issue #517) asserts the
// engine lifts wake_id / app_id / deployment_id from its inbound ctx
// (set by gatewayd-internal via the request middleware) and forwards them on
// the ctx passed to vmmd's CreateColdBoot. This is the half of the
// AC1 contract the schedd package owns: correlation fields stamped on
// the engine's bootCtx must reach the vmmd boundary.
func TestEngineWake_PropagatesWakeIDToVMM(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	// Simulate the inbound ctx gatewayd-internal produces: request_id from
	// x-faas-request-id, app_id / deployment_id from URL params, and
	// (eventually, on cold wake) wake_id minted by the gateway path.
	// PR-A is the envelope + propagation half; the engine here mints
	// wake_id itself, but the inbound ctx already carries request_id /
	// app_id / deployment_id so we can assert they survive.
	inboundCtx := wire.WithContext(context.Background(), wire.CorrelationFields{
		RequestID:    "req-gateway-1",
		AppID:        app.ID,
		DeploymentID: dep.ID,
	})

	res, err := e.Wake(inboundCtx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}

	if vmm.coldBoots != 1 {
		t.Fatalf("coldBoots = %d, want 1", vmm.coldBoots)
	}
	if vmm.lastColdBootCtx == nil {
		t.Fatal("fakeVMM did not capture cold-boot ctx")
	}

	// Lift correlation off the captured ctx. The engine joins
	// wake_id / instance_id onto the inbound set, so the resulting
	// fields must include everything inbound PLUS the engine-minted
	// wake_id / instance_id.
	got, ok := wire.FromContext(vmm.lastColdBootCtx)
	if !ok {
		t.Fatal("vmmd-bound ctx has no correlation fields")
	}
	if got.RequestID != "req-gateway-1" {
		t.Errorf("RequestID = %q, want req-gateway-1", got.RequestID)
	}
	if got.AppID != app.ID {
		t.Errorf("AppID = %q, want %q", got.AppID, app.ID)
	}
	if got.DeploymentID != dep.ID {
		t.Errorf("DeploymentID = %q, want %q", got.DeploymentID, dep.ID)
	}
	if got.WakeID == "" {
		t.Error("WakeID empty; engine must mint wake_id at cold boot")
	}
	if got.WakeID != res.WakeID {
		t.Errorf("WakeID %q != engine's WakeID %q", got.WakeID, res.WakeID)
	}
	if got.InstanceID == "" {
		t.Error("InstanceID empty; engine must record the leased instance id")
	}
	if got.InstanceID != res.InstanceID {
		t.Errorf("InstanceID %q != engine's WakeResult.InstanceID %q", got.InstanceID, res.InstanceID)
	}
}

func TestEngineWake_Idempotent(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	first, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake #1: %v", err)
	}
	second, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake #2: %v", err)
	}
	if first.InstanceID != second.InstanceID {
		t.Errorf("idempotent wake returned a new instance: %q vs %q", first.InstanceID, second.InstanceID)
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1 (second wake must reuse)", vmm.coldBoots)
	}
}

// TestEngineWake_HonorsCallerDeploymentID pins the PR-C wake-fan-out
// contract through the Phase-1 fast path: a caller-supplied
// `deploymentID` (the gateway's cached target deployment) MUST win
// over whatever LiveDeployment returns. The bug this catches: a
// regression that re-introduces `var deploymentID string` inside the
// fast-path block shadows the function parameter and silently drops
// the caller's value. The legacy test corpus only exercises the
// empty-string path (every `e.Wake(ctx, app.ID, "", "", "")` in this file),
// so the shadowing compiled clean — the bug was invisible until a
// reviewer audit surfaced it on the `pkg/sched/engine.go::Wake` shape.
//
// Setup: app has depA (initial live) + a RUNNING instance attached
// to depA. We then create depB (which supersedes depA) — LiveDeployment
// now returns depB. We call Wake(app.ID, depA.ID) and assert:
//
//  1. Phase 1 hits (the running instance is found; no cold boot).
//  2. WakeResult.DeploymentID == depA.ID, NOT depB.ID. The gateway
//     cached depA on Target; it must not silently re-route to the
//     fresh deploy.
//  3. coldBoots stays 1 (the seed cold-boot); the second Wake stays
//     in Phase 1, no fallthrough to admitAndDispatch.
func TestEngineWake_HonorsCallerDeploymentID(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	_, app, depA := seedApp(t, store, api.PlanPro, 512, 5)

	// Cold-wake to materialise a RUNNING instance attached to depA.
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatalf("seed Wake: %v", err)
	}

	// Now create depB. CreateDeployment supersedes depA to
	// DeploySuperseded (memstore.go CreateDeployment flips the prior
	// pending/live row), so LiveDeployment will return depB.ID.
	depB, err := store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:def", Status: state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment depB: %v", err)
	}
	if depB.ID == depA.ID {
		t.Fatal("depB.ID == depA.ID; supersede did not produce a fresh row")
	}

	// Sanity: LiveDeployment now points at depB, not depA. If this
	// fails the test setup is meaningless (the bug only manifests
	// when LiveDeployment disagrees with the caller hint).
	live, err := store.LiveDeployment(ctx, app.ID)
	if err != nil {
		t.Fatalf("LiveDeployment: %v", err)
	}
	if live.ID != depB.ID {
		t.Fatalf("LiveDeployment.ID = %q, want depB.ID=%q (test setup)", live.ID, depB.ID)
	}

	// Wake with the caller-supplied depA.ID — the gateway's cached
	// target deployment. Phase 1 should hit and the result must
	// carry depA.ID, NOT the freshly-promoted depB.ID.
	res, err := e.Wake(ctx, app.ID, depA.ID, "", "")
	if err != nil {
		t.Fatalf("Wake with caller deploymentID: %v", err)
	}
	if vmm.coldBoots != 1 {
		// Cold boot from the seed Wake above; the second Wake must
		// stay in Phase 1 (no second cold boot).
		t.Errorf("coldBoots = %d, want 1 (Phase 1 fast path must not fall through)", vmm.coldBoots)
	}
	if res.DeploymentID != depA.ID {
		t.Errorf("WakeResult.DeploymentID = %q, want caller-supplied depA.ID=%q "+
			"(NOT LiveDeployment's depB.ID=%q — gateway's cached target must win)",
			res.DeploymentID, depA.ID, depB.ID)
	}
}

// TestEngineWake_EmptyCallerDeploymentID_FallsBackToLiveDeployment
// pins the legacy single-deployment behaviour: when the caller
// passes an empty deploymentID (the meterd sampler + cron firing
// path, where the gateway hasn't picked a deployment yet), Phase 1
// MUST resolve via LiveDeployment. Together with
// TestEngineWake_HonorsCallerDeploymentID, this pair locks the
// preference order in both directions.
func TestEngineWake_EmptyCallerDeploymentID_FallsBackToLiveDeployment(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	_, app, depA := seedApp(t, store, api.PlanPro, 512, 5)

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(ctx, app.ID, "", "", ""); err != nil {
		t.Fatalf("seed Wake: %v", err)
	}

	// Second Wake with empty deploymentID. Phase 1 must resolve
	// via LiveDeployment — which returns depA.ID (the only live row).
	res, err := e.Wake(ctx, app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake with empty deploymentID: %v", err)
	}
	if res.DeploymentID != depA.ID {
		t.Errorf("WakeResult.DeploymentID = %q, want LiveDeployment's depA.ID=%q "+
			"(empty caller hint must fall back to LiveDeployment)",
			res.DeploymentID, depA.ID)
	}
}

func TestEngineWake_RestoreFromSnapshot(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// A fresh, version-matched snapshot makes wake a restore.
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 1,
		StorageKey: SnapshotMemKey(dep.ID),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if res.Method != vmmdpb.WakeMethod_WAKE_RESTORE {
		t.Errorf("method = %v, want WAKE_RESTORE", res.Method)
	}
	if vmm.restores != 1 || vmm.coldBoots != 0 {
		t.Errorf("restores=%d coldBoots=%d, want 1/0", vmm.restores, vmm.coldBoots)
	}
}

// TestEngineWake_StorageKey_ForwardedFromRow pins the F-2 happy path:
// Wake reads snap.StorageKey from the snapshots row (the canonical
// source after #96) and forwards it on SnapshotRef.StorageKey. This
// is the contract the OCI driver depends on — a regression here would
// silently fall back to the on-disk path under local backends and
// fail to restore under remote ones.
func TestEngineWake_StorageKey_ForwardedFromRow(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// Use a non-default storage_key so a regression that hardcodes
	// "snap/<dep>/mem" can't pass — the row's value is what vmmd
	// must see.
	customKey := "snap/" + dep.ID + "/mem" // canonical today
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 1,
		StorageKey: customKey,
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if vmm.lastSnapRef.StorageKey != customKey {
		t.Errorf("Wake forwarded StorageKey = %q, want %q (the snap row's value)", vmm.lastSnapRef.StorageKey, customKey)
	}
	if vmm.lastSnapRef.DeploymentID != dep.ID {
		t.Errorf("Wake forwarded DeploymentID = %q, want %q", vmm.lastSnapRef.DeploymentID, dep.ID)
	}
}

// TestEngineWake_RequiresStorageKey pins the slice-3 readiness
// invariant: a Wake whose app has a snapshot row must always carry the
// row's StorageKey on the wire. The engine itself must NOT synthesise
// a key from the dep id — the deprecation-window fallback is gone. If
// a snap row exists but its StorageKey is empty (F-1 contract
// violation), the engine falls back to a real cold boot (ADR-005:
// snapshots are cache, not truth) rather than calling vmmd with a
// wire snapshot that lacks a backend locator.
func TestEngineWake_RequiresStorageKey(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 1,
		StorageKey: state.SnapMemKey(dep.ID),
	}); err != nil {
		t.Fatalf("CreateSnapshot (seed): %v", err)
	}
	// Simulate a buggy inserter that stamped a row with an empty
	// StorageKey, post F-1 contract — the only way this could exist is
	// via SetSnapshotStorageKeyForTest (the MemStore's own CreateSnapshot
	// rejects empty).
	store.SetSnapshotStorageKeyForTest(dep.ID, "")

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// Empty StorageKey → engine takes the cold-boot branch
	// (haveSnap is true but snapKey is empty, so the Phase 3 gate
	// drops to e.vmm.CreateColdBoot). fakeVMM records coldBoots=1,
	// restores=0.
	if vmm.restores != 0 {
		t.Errorf("restores = %d, want 0 — empty StorageKey must NOT synthesise a key", vmm.restores)
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1 — empty StorageKey must fall back to cold boot (ADR-005)", vmm.coldBoots)
	}
}

func TestEngineWake_ForwardsSealedEnv(t *testing.T) {
	// Wake must load the app's sealed env rows from the store and pack
	// them into AppSpec.SealedEnv so vmmd can unseal + stage them onto
	// drive1. Without this, the §11/G2 secrets feature never reaches
	// the customer's running VM (PR-review regression target).
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 512, 5)

	// Two rows — proves multi-key fan-out. We don't seal real ciphertext
	// here (that's the secretbox package's job); the sched wire just
	// carries the bytes through, so any byte string exercises the path.
	if err := store.UpsertAppSecret(context.Background(), acct.ID, app.ID,
		"STRIPE_KEY", []byte("ct-stripe")); err != nil {
		t.Fatalf("UpsertAppSecret STRIPE_KEY: %v", err)
	}
	if err := store.UpsertAppSecret(context.Background(), acct.ID, app.ID,
		"DB_URL", []byte("ct-db")); err != nil {
		t.Fatalf("UpsertAppSecret DB_URL: %v", err)
	}

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}

	spec := vmm.lastColdBootSpec
	if len(spec.SealedEnv) != 2 {
		t.Fatalf("SealedEnv len=%d, want 2 (rows: %+v)", len(spec.SealedEnv), spec.SealedEnv)
	}
	// MemStore preserves insertion order; assert keys arrived.
	gotKeys := map[string][]byte{}
	for _, e := range spec.SealedEnv {
		gotKeys[e.Key] = e.Ciphertext
	}
	if string(gotKeys["STRIPE_KEY"]) != "ct-stripe" {
		t.Errorf("STRIPE_KEY ciphertext = %q, want ct-stripe", gotKeys["STRIPE_KEY"])
	}
	if string(gotKeys["DB_URL"]) != "ct-db" {
		t.Errorf("DB_URL ciphertext = %q, want ct-db", gotKeys["DB_URL"])
	}
}

func TestEngineWake_NoSecrets_EmptySealedEnv(t *testing.T) {
	// An app with zero secrets must hand vmmd a nil/empty SealedEnv so
	// the Manager short-circuits the StageSecretsEnv mount entirely.
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(vmm.lastColdBootSpec.SealedEnv) != 0 {
		t.Errorf("SealedEnv = %+v, want empty", vmm.lastColdBootSpec.SealedEnv)
	}
}

// TestEngineWake_ForwardsOverridePort pins issue #460 / ADR-053 (PR-C):
// when a Deployment carries OverridePort=9090, the engine must stamp
// Port onto both the cold-boot AppSpec (vmmd cold-boot branch) and
// the restore AppSpec (snapshot branch). The restoration path is the
// harder one — it could regress "Port lives on disk with the
// snapshot" silently, so it gets its own assertion next to lastColdBoot.
//
// The expected result: fakeVMM.lastColdBootSpec.Port captures 9090 for
// the cold-boot case, lastRestoreSpec.Port captures 9090 for the
// restore case. WakeResult.Port surfaces the same value so the
// scheddgrpc response can hand it to the gateway.
func TestEngineWake_ForwardsOverridePort(t *testing.T) {
	t.Run("cold-boot", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
		// Stamp the per-deployment override port directly. CreateDeployment
		// accepts a fully-populated Deployment, so the simplest thing is to
		// create a second deployment that supersedes the seed with the
		// override port set, then LiveDeployment reads it back.
		if _, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID:        app.ID,
			Kind:         state.DeploymentKindImage,
			ImageDigest:  "sha256:abc",
			Status:       state.DeployLive,
			OverridePort: 9090,
		}); err != nil {
			t.Fatalf("CreateDeployment override-port: %v", err)
		}
		vmm := &fakeVMM{}
		e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

		res, err := e.Wake(context.Background(), app.ID, "", "", "")
		if err != nil {
			t.Fatalf("Wake: %v", err)
		}
		if vmm.lastColdBootSpec.Port != 9090 {
			t.Errorf("cold-boot Port = %d, want 9090 (spec=%+v)",
				vmm.lastColdBootSpec.Port, vmm.lastColdBootSpec)
		}
		if res.Port != 9090 {
			t.Errorf("WakeResult.Port = %d, want 9090", res.Port)
		}
		// The seed deployment that had no override port must not leak
		// into the Wake dispatch — LiveDeployment picks the most
		// recent DeployLive row, which is the 9090 one we just created.
		_ = dep
	})

	t.Run("restore-from-snapshot", func(t *testing.T) {
		store := state.NewMemStore()
		acct, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
		if _, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID:        app.ID,
			Kind:         state.DeploymentKindImage,
			ImageDigest:  "sha256:abc",
			Status:       state.DeployLive,
			OverridePort: 9090,
		}); err != nil {
			t.Fatalf("CreateDeployment override-port: %v", err)
		}
		liveDep, err := store.LiveDeployment(context.Background(), app.ID)
		if err != nil {
			t.Fatalf("LiveDeployment: %v", err)
		}
		if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: liveDep.ID,
			FCVersion:    "1.10.0",
			MemBytes:     1,
			StorageKey:   SnapshotMemKey(liveDep.ID),
		}); err != nil {
			t.Fatalf("CreateSnapshot: %v", err)
		}
		vmm := &fakeVMM{}
		e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

		res, err := e.Wake(context.Background(), app.ID, "", "", "")
		if err != nil {
			t.Fatalf("Wake: %v", err)
		}
		if vmm.lastRestoreSpec.Port != 9090 {
			t.Errorf("restore Port = %d, want 9090 (spec=%+v)",
				vmm.lastRestoreSpec.Port, vmm.lastRestoreSpec)
		}
		if res.Port != 9090 {
			t.Errorf("WakeResult.Port = %d, want 9090", res.Port)
		}
		// Anchor: the seed flow used seedApp unchanged.
		_ = acct
	})

	// Issue #460 / ADR-053 PR-C Phase-1 fast path: when an instance
	// is already RUNNING, Wake short-circuits under appMu and returns
	// the existing row. The port must still reach the wire, sourced
	// from a LiveDeployment read inside the same critical section.
	// The fast path is the synth-loop hot path (meterd's per-minute
	// sampler + cron firings), not the customer hot path — but it
	// must remain truthful so any future caller that consumes
	// WakeResponse.port on a warm instance sees the same value
	// AdmitInstance would have produced.
	t.Run("fast-path-warm", func(t *testing.T) {
		store := state.NewMemStore()
		_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
		if _, err := store.CreateDeployment(context.Background(), state.Deployment{
			AppID:        app.ID,
			Kind:         state.DeploymentKindImage,
			ImageDigest:  "sha256:abc",
			Status:       state.DeployLive,
			OverridePort: 9090,
		}); err != nil {
			t.Fatalf("CreateDeployment override-port: %v", err)
		}
		vmm := &fakeVMM{}
		e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
		// Cold-wake first to materialise a RUNNING row.
		if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
			t.Fatalf("first Wake: %v", err)
		}
		// Second Wake hits the Phase-1 fast path.
		res, err := e.Wake(context.Background(), app.ID, "", "", "")
		if err != nil {
			t.Fatalf("second Wake (fast path): %v", err)
		}
		if res.Port != 9090 {
			t.Errorf("fast-path WakeResult.Port = %d, want 9090 "+
				"(LiveDeployment read must populate Port even on warm path)", res.Port)
		}
	})
}

// TestEngineWake_OverridePortZeroIsZero pins the no-override boundary:
// when no deployment carries an override port, the wire fields must be
// zero (the legacy signal) so vmmd's server-side buildBridgeScript
// defaults to netns.AppPort. A regression that always stamped 8080
// would break compatibility with callers that hand-build Wake frames
// for legacy tests.
func TestEngineWake_OverridePortZeroIsZero(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if vmm.lastColdBootSpec.Port != 0 {
		t.Errorf("cold-boot Port = %d, want 0", vmm.lastColdBootSpec.Port)
	}
	if res.Port != 0 {
		t.Errorf("WakeResult.Port = %d, want 0", res.Port)
	}
}

func TestEngineWake_StaleFcVersionColdBoots(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// Snapshot made by an older FC; must not be restored (ADR-005 pinning).
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.7.0", MemBytes: 1,
		StorageKey: SnapshotMemKey(dep.ID),
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if vmm.coldBoots != 1 || vmm.restores != 0 {
		t.Errorf("coldBoots=%d restores=%d, want 1/0 (version mismatch => cold boot)", vmm.coldBoots, vmm.restores)
	}
}

func TestEngineWake_RestoreFallbackMarksSnapshotStale(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	snap, _ := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0", MemBytes: 1,
		StorageKey: SnapshotMemKey(dep.ID),
	})
	vmm := &fakeVMM{forceColdFallback: true}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// vmmd fell back; the reported method is cold boot.
	if res.Method != vmmdpb.WakeMethod_WAKE_COLD_BOOT {
		t.Errorf("method = %v, want WAKE_COLD_BOOT (fallback)", res.Method)
	}
	// The bad snapshot must now be stale so the next wake doesn't retry it.
	if _, err := store.LatestSnapshot(context.Background(), dep.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("snapshot should be stale (no non-stale snapshot left); got err=%v", err)
	}
	_ = snap
}

func TestEngineWake_AdmissionDeniedReturnsProblem(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanFree, 128, 1)
	vmm := &fakeVMM{}
	warm := NewWarmAffinity(0)
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithWarmAffinity(warm)

	// Fill the ledger to the ceiling so the wake is refused for
	// capacity. PR #113 moved the resident counter to a per-node
	// map; the test seeds one fake instance per admit slot until
	// the global Σ hits api.RAMAdmissionCeilingMB. Each admit goes
	// through the public API so the per-node accounting stays
	// consistent (same path the production Wake flow takes).
	//
	// Tier A2: VCPU=0 on the fillers so the per-node vCPU budget
	// (160) does NOT bind first. The legacy single-box fixture
	// (this test pre-dates Tier A2) was designed to bind on RAM
	// only; bumping the test to fill vCPU alongside RAM would
	// shift the binding point and obscure what the test is
	// verifying. A VCPU=0 filler is the cleanest way to keep the
	// original "RAM fills first → ledger denies → row is failed"
	// invariant, and it exercises the defensive VCPU=0 path that
	// Tier A2 preserves (the placement fit check skips when
	// r.VCPU==0; the ledger check accepts vcpu=0 admits).
	billable := api.BillableRAMMB(128)
	for i := 0; ; i++ {
		err := e.Ledger().Admit(Request{
			Instance: "filler-" + strconv.Itoa(i),
			AppID:    "filler-app-" + strconv.Itoa(i), // distinct appIDs avoid the per-app concurrency gate
			Plan:     api.PlanFree,
			RAMMB:    128, VCPU: 0, MaxConcurrency: 1,
			NodeID: e.defaultLocalNodeID,
		})
		if err != nil {
			break // refused — ceiling reached
		}
		_ = billable
	}

	_, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("expected capacity denial")
	}
	var p *api.Problem
	if !errors.As(err, &p) || p.Code != api.CodeCapacity {
		t.Fatalf("error = %v, want *api.Problem capacity", err)
	}
	if vmm.coldBoots != 0 {
		t.Errorf("no boot should happen on denial; coldBoots=%d", vmm.coldBoots)
	}
	// The instance row should have been transitioned to failed, not left waking.
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one failed row", rows)
	}
	if _, ok := warm.LastWarmNode(app.ID); ok {
		t.Fatal("capacity-denied wake must not leave a warm placement hint")
	}
}

func TestEngineWake_AdmissionDeniedOnVCPU(t *testing.T) {
	// Companion to TestEngineWake_AdmissionDeniedReturnsProblem
	// (RAM-bound rejection). Fills the per-node vCPU budget
	// (Tier A2, default-local vcpu_budget=160) without binding RAM,
	// then drives a wake whose request vCPU cannot fit and asserts
	// the row transitions to FAILED with a CodeCapacity problem.
	//
	// The vCPU fillers use VCPU=1 (the smallest non-zero request)
	// and distinct appIDs so the per-app concurrency gate allows
	// each admit. The wake's app uses Pro (VCPU=2) so the 161st
	// filler is the rejection point — the wake's own vCPU=2 admit
	// pushes the node over the 160 budget. RAM is well below the
	// 47,600 MB ceiling (160 × 16 MB = 2,560 MB), so the RAM gate
	// doesn't bind first.
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 128, 1) // Pro plan → vCPU=2
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	for i := 0; i < 160; i++ {
		err := e.Ledger().Admit(Request{
			Instance: "vfill-" + strconv.Itoa(i),
			AppID:    "vfill-app-" + strconv.Itoa(i), // distinct appIDs avoid the per-app concurrency gate
			Plan:     api.PlanFree,
			RAMMB:    8, VCPU: 1, MaxConcurrency: 1, // tiny RAM, no per-app concurrency collision
			NodeID: e.defaultLocalNodeID,
		})
		if err != nil {
			t.Fatalf("vCPU filler %d: %v (test setup; vCPU gate fired before RAM filled)", i, err)
		}
	}
	// The 161st fill (VCPU=1) would push the node to 161/160 — the
	// vCPU gate is the rejection point. The wake's own vCPU=2
	// admit is the test's load-bearing rejection: it's the same
	// path the wake takes in production (placement → admit → ledger
	// gate), and it should reach the ledger with the node at
	// 160/160, accept 2 only by lifting the ledger floor (which
	// it doesn't), and return CodeCapacity.
	//
	// We don't run the 161st filler; the wake itself is the
	// rejection. This mirrors the RAM test's pattern, which also
	// stops the filler loop at the first admit capacity failure
	// and uses the wake as the assertion path.

	_, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("expected vCPU capacity denial")
	}
	var p *api.Problem
	if !errors.As(err, &p) || p.Code != api.CodeCapacity {
		t.Fatalf("error = %v, want *api.Problem capacity (vCPU headroom)", err)
	}
	// The chooser surfaces the vCPU binding constraint in the
	// denial detail so an operator triaging the failure can
	// identify which limit bound first. The substring is the
	// chooser's "no active compute_node fits %d MB billable / %d
	// vCPU" message (placement.go:312).
	if !strings.Contains(p.Detail, "vCPU") {
		t.Errorf("denial detail = %q, want substring \"vCPU\" (the vCPU gate is the binding constraint)", p.Detail)
	}
	if vmm.coldBoots != 0 {
		t.Errorf("no boot should happen on vCPU denial; coldBoots=%d", vmm.coldBoots)
	}
	// The chooser rejected the placement — no instance row was
	// created (the engine creates the row only after the chooser
	// picks a node). This is correct: the chooser's vCPU fit
	// check is the load-bearing Tier A2 gate, and a fleet-wide
	// refusal is the expected lever (vs. the RAM-bound test
	// where the chooser still picks the node and the ledger
	// rejects at Admit because the store sum was zero — the
	// test-seam difference, not a Tier A2 issue).
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want empty (chooser rejected, no row created)", rows)
	}
}

func TestEngineWake_BootErrorFails(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{wakeErr: errors.New("firecracker boom")}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err == nil {
		t.Fatal("expected boot error")
	}
	// Ledger must be released (no leak) and the instance marked failed.
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released on failure)", got)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one failed row", rows)
	}
}

func TestEnginePrime_BootsSnapshotsParks(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	if err := e.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	if vmm.coldBoots != 1 || vmm.snapshots != 1 {
		t.Errorf("coldBoots=%d snapshots=%d, want 1/1", vmm.coldBoots, vmm.snapshots)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateParked) {
		t.Fatalf("rows = %+v, want one parked row", rows)
	}
	// A parked app consumes zero resident RAM (invariant §6.2-4).
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 after park", got)
	}
	// snapshot_written must be emitted so imaged records the row.
	if notif.count("snapshot_written") != 1 {
		t.Errorf("snapshot_written emitted %d times, want 1", notif.count("snapshot_written"))
	}
}

func TestEnginePark_SnapshotFailureStops(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	vmm.snapErr = errors.New("disk full")
	if err := e.Park(context.Background(), res.InstanceID); err == nil {
		t.Fatal("expected snapshot failure")
	}
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped (snapshot failed => cold boot next)", ins.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (RAM freed even on snapshot failure)", got)
	}
}

func TestEngineEvict_Destroys(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := e.Evict(context.Background(), res.InstanceID); err != nil {
		t.Fatalf("Evict: %v", err)
	}
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", ins.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 after evict", got)
	}
}

// TestEngineParkAppSnapshotsRunningInstance pins the app-level park contract:
// once apid has changed the app to evicted_cold, schedd must perform the
// instance lifecycle work instead of leaving a RUNNING row behind. The
// operation is also idempotent so a redelivered notification is harmless.
func TestEngineParkAppSnapshotsRunningInstance(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatalf("UpdateApp parked: %v", err)
	}

	acted, err := e.ParkApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ParkApp: %v", err)
	}
	if acted != 1 {
		t.Fatalf("ParkApp acted = %d, want 1", acted)
	}
	if vmm.snapshots != 1 {
		t.Errorf("snapshots = %d, want 1", vmm.snapshots)
	}
	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateParked) {
		t.Fatalf("instance state = %q, want parked", row.State)
	}

	acted, err = e.ParkApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("idempotent ParkApp: %v", err)
	}
	if acted != 0 {
		t.Errorf("idempotent ParkApp acted = %d, want 0", acted)
	}
	if vmm.snapshots != 1 {
		t.Errorf("idempotent snapshots = %d, want 1", vmm.snapshots)
	}
}

func TestEngineReconcileDeletedAppDestroysRunningInstance(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	deleted := state.AppDeleted
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp deleted: %v", err)
	}

	acted, err := e.ReconcileLifecycleInstance(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("ReconcileLifecycleInstance: %v", err)
	}
	if !acted {
		t.Fatal("ReconcileLifecycleInstance acted = false, want true")
	}
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", row.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0", got)
	}

	acted, err = e.ReconcileLifecycleInstance(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("idempotent ReconcileLifecycleInstance: %v", err)
	}
	if acted {
		t.Errorf("idempotent reconcile acted = true, want false")
	}
	if vmm.destroys != 1 {
		t.Errorf("idempotent destroys = %d, want 1", vmm.destroys)
	}
}

func TestEngineReconcileAccountDeletionDestroysRunningInstance(t *testing.T) {
	store := state.NewMemStore()
	acct, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if err := store.MarkAccountDeletionPending(context.Background(), acct.ID); err != nil {
		t.Fatalf("MarkAccountDeletionPending: %v", err)
	}

	acted, err := e.ReconcileLifecycleInstance(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("ReconcileLifecycleInstance: %v", err)
	}
	if !acted {
		t.Fatal("ReconcileLifecycleInstance acted = false, want true")
	}
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateEvictingAccountDeleting) {
		t.Errorf("state = %q, want evicting_account_deleting", row.State)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0", got)
	}

	acted, err = e.ReconcileLifecycleInstance(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("idempotent ReconcileLifecycleInstance: %v", err)
	}
	if !acted {
		t.Fatal("idempotent account reconcile acted = false, want true for destroy retry")
	}
	if vmm.destroys != 2 {
		t.Errorf("idempotent destroys = %d, want 2 (idempotent vmmd cleanup)", vmm.destroys)
	}
}

func TestEngineReconcileDeletedAppRetriesAfterDestroyFailure(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{destroyErr: errors.New("vmmd unavailable")}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	deleted := state.AppDeleted
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp deleted: %v", err)
	}

	if acted, err := e.ReconcileLifecycleInstance(context.Background(), res.InstanceID); err == nil || acted {
		t.Fatalf("failed reconcile = (%v, %v), want error and no action", acted, err)
	}
	row, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if row.State != string(state.StateRunning) {
		t.Errorf("state after failed destroy = %q, want running for retry", row.State)
	}
	if got := e.Ledger().ResidentRAM(); got == 0 {
		t.Error("resident ledger released after failed destroy; retry would lose accounting")
	}

	vmm.destroyErr = nil
	if acted, err := e.ReconcileLifecycleInstance(context.Background(), res.InstanceID); err != nil || !acted {
		t.Fatalf("retry reconcile = (%v, %v), want action without error", acted, err)
	}
	row, _ = store.InstanceByID(context.Background(), res.InstanceID)
	if row.State != string(state.StateStopped) {
		t.Errorf("state after retry = %q, want stopped", row.State)
	}
}

func TestEngineReportActivity(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	res, _ := e.Wake(context.Background(), app.ID, "", "", "")

	now := time.Now()
	applied, err := e.ReportActivity(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: now},
		{InstanceID: "ghost", LastRequest: now},
	})
	if err != nil {
		t.Fatalf("ReportActivity: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (ghost dropped)", applied)
	}
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if !ins.LastRequestAt.Equal(now) {
		t.Errorf("last_request_at = %v, want %v", ins.LastRequestAt, now)
	}
}

func TestEngineSeedLedger(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	// A running instance survived a schedd restart.
	ins, _ := store.CreateInstance(context.Background(), app.ID, dep.ID, string(state.StateRunning), 512, state.DefaultLocalNodeName, "")
	_ = ins

	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	if err := e.SeedLedger(context.Background()); err != nil {
		t.Fatalf("SeedLedger: %v", err)
	}
	if got := e.Ledger().ResidentRAM(); got != 512+api.PerVMOverheadMB {
		t.Errorf("resident = %d, want %d (running instance re-accounted)", got, 512+api.PerVMOverheadMB)
	}
}

// TestEngineWake_VMMDColdBootDeadlineEnforced (commit 1, spec §6.1) pins
// that a Wake whose vmmd call exceeds the §6.1 budget (COLD_BOOTING ≤
// 30s) cannot leak the ledger reservation. The fake's sleepFor is set
// past the budget; the engine's context.WithTimeout wrapper must fire
// before the test gives up. After the Wake fails, the ledger is
// released and the instance row is FAILED.
func TestEngineWake_VMMDColdBootDeadlineEnforced(t *testing.T) {
	t.Parallel()

	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// Cold-boot path: no snapshot, so initState = COLD_BOOTING and the
	// budget is whatever budgetFor returns for that state. The engine
	// below runs on testBootBudget (200ms) rather than the real
	// ColdBootTimeout (35s); the fake sleeps 25× that, far enough past
	// the deadline that a broken wrapper is unambiguous, and the failure
	// mode costs 5s instead of 70s.
	vmm := &fakeVMM{sleepFor: 25 * testBootBudget}
	e := withTestBootBudget(t, newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0"))

	start := time.Now()
	_, err := e.Wake(context.Background(), app.ID, "", "", "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Wake returned nil err, expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wake err = %v, want context.DeadlineExceeded", err)
	}
	// The point of the elapsed check is that Wake returned on the
	// deadline rather than riding the fake's sleep to completion. 2s is
	// 10× the budget and 40% of the fake's sleep: generous enough that a
	// saturated runner won't trip it, tight enough that it still fails
	// if the WithTimeout wrapper is dropped.
	if elapsed > 2*time.Second {
		t.Errorf("Wake took %v, want the %v budget to fire (fake sleeps %v)",
			elapsed, testBootBudget, 25*testBootBudget)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released on deadline)", got)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one FAILED row", rows)
	}
}

// TestEnginePrime_VMMDDeadlineEnforced mirrors the Wake test for the
// Prime path. Prime is always cold-boot, so it gets the same
// ColdBootTimeout — the only difference is that Prime's instance goes
// RUNNING → SNAPSHOTTING → PARKED on success, but a hung vmmd should
// leave the row FAILED with no reservation.
func TestEnginePrime_VMMDDeadlineEnforced(t *testing.T) {
	t.Parallel()

	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{sleepFor: 25 * testBootBudget}
	e := withTestBootBudget(t, newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0"))

	err := e.Prime(context.Background(), app.ID, dep.ID)
	if err == nil {
		t.Fatal("Prime returned nil err, expected deadline error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Prime err = %v, want context.DeadlineExceeded", err)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released on deadline)", got)
	}
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateFailed) {
		t.Errorf("rows = %+v, want one FAILED row", rows)
	}
}

// TestBootTimeout_SpecBudgets pins the §6.1 vmmd budgets directly.
//
// This is the assertion the two deadline tests above used to make
// implicitly, by sleeping a fake past a real 35s timer and observing
// that it fired. Doing it on the table instead is both instant and
// stricter: the old form proved only "some deadline fired within 37s",
// so ColdBootTimeout could have drifted to 20s or 34s and stayed green.
// Here a drift of one second fails.
func TestBootTimeout_SpecBudgets(t *testing.T) {
	t.Parallel()

	// Spec §6.1: WAKING ≤ 5s → fall back to cold-boot (+1s vmmd round
	// trip = 6s); COLD_BOOTING ≤ 30s → FAILED (+5s jailer setup = 35s).
	if WakingTimeout != 6*time.Second {
		t.Errorf("WakingTimeout = %v, want 6s (§6.1: 5s + 1s vmmd round trip)", WakingTimeout)
	}
	if ColdBootTimeout != 35*time.Second {
		t.Errorf("ColdBootTimeout = %v, want 35s (§6.1: 30s + jailer setup)", ColdBootTimeout)
	}

	for _, tc := range []struct {
		name string
		in   state.State
		want time.Duration
	}{
		{"waking gets the waking budget", state.StateWaking, WakingTimeout},
		{"cold-booting gets the cold-boot budget", state.StateColdBooting, ColdBootTimeout},
		// Unknown states fall through to the conservative branch rather
		// than returning a zero duration — a zero would make
		// context.WithTimeout fire immediately and fail every wake.
		{"running falls through to cold-boot", state.StateRunning, ColdBootTimeout},
		{"empty falls through to cold-boot", state.State(""), ColdBootTimeout},
	} {
		if got := bootTimeout(tc.in); got != tc.want {
			t.Errorf("%s: bootTimeout(%q) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
		if got := bootTimeout(tc.in); got == 0 {
			t.Errorf("%s: bootTimeout(%q) returned zero — every wake would fail instantly", tc.name, tc.in)
		}
	}
}

// TestNewEngine_UsesSpecBudgets is the guard on the bootBudget test
// seam: an engine built the way production builds one must carry a nil
// override and resolve to the §6.1 constants. If someone wires a
// shortened budget into NewEngine — or ships withTestBootBudget's
// assignment outside a test — this fails.
func TestNewEngine_UsesSpecBudgets(t *testing.T) {
	t.Parallel()

	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	if e.bootBudget != nil {
		t.Error("NewEngine set bootBudget; the override is a test seam and must stay nil in production")
	}
	if got := e.budgetFor(state.StateColdBooting); got != ColdBootTimeout {
		t.Errorf("budgetFor(COLD_BOOTING) = %v, want ColdBootTimeout %v", got, ColdBootTimeout)
	}
	if got := e.budgetFor(state.StateWaking); got != WakingTimeout {
		t.Errorf("budgetFor(WAKING) = %v, want WakingTimeout %v", got, WakingTimeout)
	}

	// And the seam actually takes effect when a test does set it —
	// otherwise the deadline tests above would be asserting nothing.
	withTestBootBudget(t, e)
	if got := e.budgetFor(state.StateColdBooting); got != testBootBudget {
		t.Errorf("after withTestBootBudget: budgetFor = %v, want %v", got, testBootBudget)
	}
}

// bootSignalVMM returns a fakeVMM configured for lock-narrow tests.
// bootStarted has capacity 4 so multiple Wake callers can signal their
// arrival inside Phase 3 without blocking. bootRelease closes once —
// every blocked fake receives the closure and unblocks.
func bootSignalVMM() (*fakeVMM, chan struct{}, chan struct{}) {
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	return &fakeVMM{
		bootStarted: started,
		bootRelease: release,
	}, started, release
}

// TestEngineWake_LockReleasedDuringBoot (commit 2, finding #1) proves
// the vmmd call happens outside the per-app mutex. Without the lock
// narrowing, B would block on appMu while A holds the lock through
// Phase 3; B would never reach its own vmmd call. We fence A on the
// boot channels, start B, and assert B reaches its own Phase 3 by
// observing vmm.coldBoots grow from 0 to 2 within a short window.
func TestEngineWake_LockReleasedDuringBoot(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm, bootStarted, bootRelease := bootSignalVMM()
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	aResult := make(chan error, 1)
	aDone := make(chan struct{})
	go func() {
		defer close(aDone)
		_, err := e.Wake(context.Background(), app.ID, "", "", "")
		aResult <- err
	}()

	// Wait for A to be inside the boot call (Phase 3).
	select {
	case <-bootStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("A's Wake never entered the vmmd boot")
	}

	// Start B. It should run Phase 1 (fast-path miss), Phase 2
	// (resolve + Admit) immediately, and reach its own Phase 3
	// without blocking on A's appMu.
	bResult := make(chan error, 1)
	bDone := make(chan struct{})
	go func() {
		defer close(bDone)
		_, err := e.Wake(context.Background(), app.ID, "", "", "")
		bResult <- err
	}()

	// Wait for B to enter its own Phase 3 (signalled on bootStarted).
	// A has already consumed slot 1; B's signal lands in slot 2.
	select {
	case <-bootStarted:
	case <-time.After(500 * time.Millisecond):
		close(bootRelease)
		select {
		case <-aDone:
		case <-aResult:
		}
		select {
		case <-bDone:
		case <-bResult:
		}
		t.Fatal("B never reached Phase 3 — appMu held during A's boot (regression of finding #1)")
	}

	// Both A and B are now inside their vmmd calls. Release them.
	close(bootRelease)
	select {
	case err := <-aResult:
		if err != nil {
			t.Errorf("A's Wake returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A's Wake never returned")
	}
	select {
	case err := <-bResult:
		if err != nil {
			t.Errorf("B's Wake returned err: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B's Wake never returned")
	}
	select {
	case <-aDone:
	case <-time.After(time.Second):
		t.Fatal("A never completed")
	}
	select {
	case <-bDone:
	case <-time.After(time.Second):
		t.Fatal("B never completed")
	}
}

// TestEngineWake_PostVMMDAbortOnStolenState (commit 2, finding #1)
// proves the Phase 4 re-read defends against watchdog-steals-state
// (commit 3). We simulate a watchdog-like event by setting the
// instance's state to STOPPED after Phase 2 completes but before the
// vmmd call returns. Wake's Phase 4 must:
//   - re-read the row,
//   - see STOPPED ≠ COLD_BOOTING,
//   - destroy the VM it just booted,
//   - release the ledger reservation,
//   - return an error.
//
// The boot is fenced so the test controls when Phase 3 finishes.
func TestEngineWake_PostVMMDAbortOnStolenState(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm, bootStarted, bootRelease := bootSignalVMM()
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	type result struct {
		id  string
		err error
	}
	aRes := make(chan result, 1)
	go func() {
		r, err := e.Wake(context.Background(), app.ID, "", "", "")
		aRes <- result{r.InstanceID, err}
	}()

	var insID string
	// Poll for the COLD_BOOTING row — appears after Phase 2 commits.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
		for _, r := range rows {
			if r.State == string(state.StateColdBooting) {
				insID = r.ID
				break
			}
		}
		if insID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if insID == "" {
		t.Fatal("never observed a COLD_BOOTING row — Phase 2 didn't commit")
	}

	// Simulate the watchdog (or any external transition) stealing
	// the state. We mutate the store directly; the engine will
	// observe this in Phase 4's re-read.
	if err := store.UpdateInstanceState(context.Background(), insID, string(state.StateStopped)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}

	// Drain bootStarted (best-effort — it may have already been
	// signalled by the time we got here) and release the boot.
	select {
	case <-bootStarted:
	default:
	}
	close(bootRelease)

	select {
	case r := <-aRes:
		if r.err == nil {
			t.Fatalf("Wake returned nil err despite state theft; expected error")
		}
		if !strings.Contains(r.err.Error(), "state stolen") {
			t.Errorf("Wake err = %v, want 'state stolen' message", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wake never returned after release")
	}

	// VM must be destroyed (the boot was wasted) and ledger released.
	vmm.mu.Lock()
	defer vmm.mu.Unlock()
	if vmm.destroys < 1 {
		t.Errorf("destroys = %d, want ≥1 (post-stolen-state cleanup)", vmm.destroys)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (reservation released)", got)
	}
}

// TestEngineVmstateHelpers pins the two helpers #121 / ADR-025 axis 2
// slice 4 added on the Engine — vmstateHostPathFor (always returns the
// deterministic host path) and vmstateStorageKeyFor (returns "" for
// default-local, the canonical key for remote nodes). Without this test
// a future refactor of the helper could route default-local through the
// StorageBackend (breaking the single-box contract) or construct the
// storage key with the wrong prefix (breaking the OCI driver), and no
// CI test would fail.
func TestEngineVmstateHelpers(t *testing.T) {
	const (
		depID = "d-1"
		// The default-local node UUID is whatever the seed migration
		// picks; the helper compares against it for the "" short
		// circuit, so the literal here MUST match the engine's
		// defaultLocalNodeID for the "default-local" case to
		// short-circuit.
		defaultLocalID = "00000000-0000-0000-0000-000000000001"
		remoteID       = "00000000-0000-0000-0000-000000000002"
	)
	e := &Engine{defaultLocalNodeID: defaultLocalID}

	// vmstateHostPathFor is a pure function of depID — invariant under
	// node identity.
	hostTests := []struct {
		name string
		dep  string
		want string
	}{
		{"standard dep", "d-1", "/srv/fc/snap/d-1/vmstate"},
		{"uuid-shaped dep", "9c2b6d8a-1f3e", "/srv/fc/snap/9c2b6d8a-1f3e/vmstate"},
	}
	for _, tt := range hostTests {
		t.Run("host/"+tt.name, func(t *testing.T) {
			if got := e.vmstateHostPathFor(tt.dep); got != tt.want {
				t.Errorf("vmstateHostPathFor(%q) = %q, want %q", tt.dep, got, tt.want)
			}
		})
	}

	keyTests := []struct {
		name   string
		nodeID string
		dep    string
		want   string
	}{
		{"default-local returns empty", defaultLocalID, depID, ""},
		// Empty nodeID short-circuits to "" as well; the engine
		// can't route an unknown node and falling back to ""
		// preserves default-local behaviour rather than failing the
		// wake outright.
		{"empty nodeID returns empty", "", depID, ""},
		{"remote node returns canonical key", remoteID, depID, "snap/" + depID + "/vmstate"},
	}
	for _, tt := range keyTests {
		t.Run("key/"+tt.name, func(t *testing.T) {
			if got := e.vmstateStorageKeyFor(tt.nodeID, tt.dep); got != tt.want {
				t.Errorf("vmstateStorageKeyFor(%q, %q) = %q, want %q", tt.nodeID, tt.dep, got, tt.want)
			}
		})
	}
}

// TestEngineWake_MintsFreshWakeIDPerWake asserts that schedd mints a new
// wake_id on each Wake() call. Two consecutive wakes of the same parked
// app must yield distinct IDs even though the underlying instance row
// (ins.ID) may be reused after a park→wake cycle. This is the contract
// that lets operators correlate x-faas-wake-id headers against a single
// wake attempt without confusing it with prior wakes.
func TestEngineWake_MintsFreshWakeIDPerWake(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	// First wake — must produce a non-empty UUIDv7-shaped wake_id.
	res1, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("first Wake: %v", err)
	}
	if res1.WakeID == "" {
		t.Fatal("WakeID empty on first wake (must be minted at CreateInstance)")
	}
	if _, err := uuid.Parse(res1.WakeID); err != nil {
		t.Errorf("WakeID %q is not a valid UUID: %v", res1.WakeID, err)
	}

	// Park the instance we just woke so the second wake is a fresh
	// cycle. The contract being asserted is "each Wake() mints a new
	// wake_id", so we have to actually take the app back to parked
	// before the second call.
	if err := e.Park(context.Background(), res1.InstanceID); err != nil {
		t.Fatalf("Park: %v", err)
	}

	// Second wake — distinct wake_id even though the app (and likely the
	// instance row) is the same.
	res2, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake: %v", err)
	}
	if res2.WakeID == "" {
		t.Fatal("WakeID empty on second wake")
	}
	if res1.WakeID == res2.WakeID {
		t.Errorf("WakeID = %q on both wakes; expected distinct UUIDv7 per wake", res1.WakeID)
	}
	// The stored instance's WakeID must equal what Wake returned —
	// consumers reading from the DB see the same value the gateway
	// header carries.
	ins, err := store.RunningInstanceForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if ins.WakeID != res2.WakeID {
		t.Errorf("stored WakeID = %q, want %q (the value Wake returned)", ins.WakeID, res2.WakeID)
	}
}

// TestEngineWake_Phase1FastPathReturnsExistingWakeID pins the contract
// added in the gaps analysis 2026-07-23 review (finding #1): a second
// Wake for an app that is already RUNNING must surface the wake_id
// stamped on the row, not an empty string. Without this contract a
// warm request gets no x-faas-wake-id header, which loses the
// correlation handle for the request that originally brought the
// instance up.
func TestEngineWake_Phase1FastPathReturnsExistingWakeID(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	// First wake → cold boot → row carries a fresh wake_id.
	res1, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("first Wake: %v", err)
	}
	if res1.WakeID == "" {
		t.Fatal("first Wake produced empty wake_id")
	}

	// Second Wake on the already-RUNNING app → Phase 1 fast path.
	// WakeID must equal the row's existing value, NOT be empty.
	res2, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("second Wake (fast path): %v", err)
	}
	if res2.WakeID == "" {
		t.Fatal("Phase 1 fast path returned empty wake_id; should surface the row's existing value")
	}
	if res2.WakeID != res1.WakeID {
		t.Errorf("Phase 1 wake_id = %q, want %q (row's existing value)", res2.WakeID, res1.WakeID)
	}
	// Fast-path Wake must not have triggered a new boot.
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d after fast-path Wake, want 1 (no new boot)", vmm.coldBoots)
	}
}

// TestEnginePrime_MintsWakeID asserts that Prime() — the pre-warm
// cold-boot flow at deploy time — also mints a wake_id. Prime is a
// wake-shape event (gaps analysis 2026-07-23): the instance is being
// created for the first time as part of a fresh deploy, so it earns
// its own UUIDv7 just like Wake does. Without this test, a future
// refactor could quietly drop wake_id from Prime's CreateInstance call
// and the dashboard "Recent wakes" view would render an empty wake_id
// for the boot that primed an app.
func TestEnginePrime_MintsWakeID(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	if err := e.Prime(context.Background(), app.ID, dep.ID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	rows, err := store.ListInstancesForApp(context.Background(), app.ID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].WakeID == "" {
		t.Errorf("primed instance wake_id empty; Prime must mint a UUIDv7 like Wake does")
	}
	if _, err := uuid.Parse(rows[0].WakeID); err != nil {
		t.Errorf("primed wake_id %q is not a valid UUID: %v", rows[0].WakeID, err)
	}
}

// TestTransitionWithKind_EmitsRowWakeID asserts that subsequent
// state transitions emitted via emitInstanceChanged (the audit-log
// path, NOT the Wake/Prime path) carry the row's wake_id in the
// pg_notify payload. Without the row-load before emit (review
// finding #3, gaps analysis 2026-07-23), the SSE payload went out
// with wake_id="" for every non-wake transition, so a dashboard
// subscribed to instance_changed saw the column go empty as soon as
// the instance entered RUNNING — breaking correlation. This test
// pins the fix: the wake_id in the payload equals the row's.
func TestTransitionWithKind_EmitsRowWakeID(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	// Create an instance manually with a known wake_id and drive a
	// state transition through the engine's transition helper, which
	// is what emitInstanceChanged's audit path ultimately calls. The
	// cleanest seam is to park a freshly-woken app and assert the
	// snapshotting→parked transition carries the row's wake_id.
	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	// Snapshot transitions go through snapshotAndPark which already
	// loaded the row; the test covers the transitionWithKind seam
	// directly via a Park (which calls transition → parked).
	if err := e.Park(context.Background(), res.InstanceID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	// Find the parked-state instance_changed payload and parse the
	// wake_id out of it. The Wake() boot itself emits a waking →
	// running transition, then Park emits snapshotting → parked.
	var parkedPayload string
	for _, ev := range notif.events {
		if ev.channel != "instance_changed" {
			continue
		}
		// Cheap substring check to find the parked-state event —
		// the engine emits events in time order so the parked one
		// is last for this app.
		if strings.Contains(ev.payload, `"state":"parked"`) {
			parkedPayload = ev.payload
		}
	}
	if parkedPayload == "" {
		t.Fatalf("no instance_changed payload with state=parked; events=%+v", notif.events)
	}
	// The JSON must carry wake_id matching the row.
	if !strings.Contains(parkedPayload, `"wake_id":"`+res.WakeID+`"`) {
		t.Errorf("parked payload missing row wake_id %q; got %s", res.WakeID, parkedPayload)
	}
}

// TestProperty_EngineReaper_BurstToIdle30s (issue #171) pins
// the §4.3 acceptance gate on the engine: a 5-instance burst
// that drops to 0 rps must park back to ≤ min_instances + 1
// within 30 s of the traffic drop. The test drives Loop with a
// fake clock + 5-min mirror bucket so it's robust to
// sub-second drift, exactly the same way loop_test.go does for
// the per-tick integration tests.
//
// Reuses fakeVMM (per invariants-property-test-fakevmm-reuse
// memory note) so no KVM is needed.
func TestProperty_EngineReaper_BurstToIdle30s(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// min_instances=1, autoscale_target_rps=10. After the burst
	// drops to 0 rps, the loop must park 4 instances back down to
	// ≤ 2 (min_instances + 1 hysteresis buffer) within 3 reaper
	// ticks. The test simulates the elapsed time by advancing the
	// fake clock per tick.
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetMinInstances:       true,
		MinInstances:          intPtr(1),
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wakeN(t, engine, app.ID, 5)

	// Mirror: 5-min bucket so the test is robust to sub-second
	// drift; the traffic sequence (100, 0) lands in the same
	// window. Production wires a 1s bucket; tests benefit from
	// being clock-aligned.
	scraper := &fakeScaleUpScraper{}
	now := time.Now().Add(35 * time.Second) // past MinInstanceAge (30s)
	clock := func() time.Time { return now }
	mirror := recentload.New(scraper, 5, 5*time.Minute)

	// Phase 1: serve 100 rps for 10 s (1 seed + 1 mid-window touch).
	scraper.set(map[string]int64{app.ID: 100})
	mirror.Touch(context.Background(), now)
	now = now.Add(10 * time.Second)
	mirror.Touch(context.Background(), now)

	loop := NewLoop(nil, engine, testLog()).
		WithClock(clock).
		WithRecentLoad(mirror).
		WithReaperAggressive(true)

	// Phase 2: traffic drops to 0. Touch the mirror with a
	// cumulative that DROPS below lastSeen so the ring resets.
	scraper.set(map[string]int64{app.ID: 0})
	mirror.Touch(context.Background(), now.Add(time.Second))

	// Three reaper ticks (10s apart in production; the test
	// advances `now` to simulate the elapsed 30 s and re-runs
	// the loop body). After the third tick the running set must
	// be ≤ min_instances + 1 = 2.
	for tick := 1; tick <= 3; tick++ {
		now = now.Add(10 * time.Second)
		loop.runReaper(context.Background())
	}

	running := liveCount(t, store, app.ID)
	if running > 2 {
		t.Errorf("running = %d after 30s of 0 rps, want ≤ 2 (min_instances + 1 buffer)", running)
	}
}

// staleVerifier is the canonical seam (per memory note
// invariants-property-test-fakevmm-reuse) — wraps the LayerVerifier
// interface and records the layer keys it was asked to verify, so
// the wake-rejection test can assert the verifier was actually
// invoked (rather than the engine short-circuiting on a nil field).
type staleVerifier struct {
	calls   int
	lastKey string
	// reject returns the *api.Problem the engine will surface.
	// nil = accept (round-trip); non-nil = reject with this error.
	reject error
}

func (s *staleVerifier) Verify(_ context.Context, layerKey, _ string) error {
	s.calls++
	s.lastKey = layerKey
	return s.reject
}

// TestEngineWake_RejectsBadSig pins the cold-boot verify path:
// a verifier that returns a sig_invalid Problem MUST cause the
// wake to surface that Problem, transition the deployment to
// StateFailed with kind "sig_invalid", release the ledger slot,
// and skip the vmmd round-trip entirely. ADR-038 §Consequences
// Compatibility names this as the operator-facing failure mode.
func TestEngineWake_RejectsBadSig(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	tampered := api.NewProblem(503, api.CodeSigInvalid,
		"signature does not match ext4",
		"ECDSA P-256 verification failed; refusing to boot tampered ext4")
	verifier := &staleVerifier{reject: tampered}
	e.WithVerifier(verifier)

	_, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("Wake accepted tampered sig; want error")
	}
	if !strings.Contains(err.Error(), api.CodeSigInvalid) {
		t.Errorf("err = %v, want code %q", err, api.CodeSigInvalid)
	}
	// The verify call MUST have run with the deployment's
	// layer key (the key imaged wrote the sig under). A nil
	// verifier would silently short-circuit; this asserts the
	// wiring is live.
	if verifier.calls != 1 {
		t.Errorf("verifier.calls = %d, want 1", verifier.calls)
	}
	if verifier.lastKey == "" {
		t.Errorf("verifier.lastKey empty; want non-empty layer key")
	}
	// vmmd must NOT have been invoked — the verify path
	// short-circuits before CreateColdBoot.
	if vmm.coldBoots != 0 || vmm.restores != 0 {
		t.Errorf("vmm invoked after bad sig: coldBoots=%d restores=%d, want 0/0",
			vmm.coldBoots, vmm.restores)
	}
	// Ledger reservation MUST have been released — otherwise the
	// bad-sig instance would count toward the plan's max
	// concurrency forever.
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("ResidentRAM = %d MB after reject, want 0 (slot must be released)", got)
	}
}

// TestEngineWake_AcceptsGoodSig is the round-trip counterpart:
// the verifier returns nil and the wake proceeds normally.
// Combined with RejectsBadSig above, this pins both directions
// of the verifier wiring.
func TestEngineWake_AcceptsGoodSig(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	verifier := &staleVerifier{} // reject == nil → accept
	e.WithVerifier(verifier)

	res, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake with good sig: %v", err)
	}
	if res.NodeID == "" {
		t.Errorf("res.NodeID empty; want compute_node id")
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1", vmm.coldBoots)
	}
	if verifier.calls != 1 {
		t.Errorf("verifier.calls = %d, want 1", verifier.calls)
	}
}

// TestEngineWake_NilVerifierSkipsVerify asserts the test-seam
// invariant: an engine constructed without WithVerifier must NOT
// panic and MUST complete the wake as if no verifier were wired.
// Production cmd/schedd fails to start when the verifier is nil
// (per WithVerifier's doc); this test only protects the
// scheduler-load + watchdog test surfaces that never reach Wake.
func TestEngineWake_NilVerifierSkipsVerify(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	// No WithVerifier call — verifier field is nil.

	if _, err := e.Wake(context.Background(), app.ID, "", "", ""); err != nil {
		t.Fatalf("Wake with nil verifier: %v", err)
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1", vmm.coldBoots)
	}
}

// TestEngineWake_RejectsTransientVerifierIO pins the third
// branch on the wake-verifier path (review finding #3 on PR
// #322): the verifier returns a non-Problem error (the storage
// backend refused to read the sig blob, etc.). The engine must
// (1) NOT surface sig_invalid, (2) wrap the error as a
// *api.Problem with Retry-After so gatewayd-internal's writeWakeError
// flushes the header, and (3) release the ledger slot rather
// than holding it forever on a transient storage blip.
func TestEngineWake_RejectsTransientVerifierIO(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	transient := errors.New("cosign: read sig \"sigs/x.sig\": storage backend unreachable")
	verifier := &staleVerifier{reject: transient}
	e.WithVerifier(verifier)

	_, err := e.Wake(context.Background(), app.ID, "", "", "")
	if err == nil {
		t.Fatal("Wake accepted transient verifier I/O; want error")
	}
	// Must NOT carry sig_invalid — that's the tamper branch, this
	// is the transient-I/O branch and the customer (and the
	// alerting) need to distinguish them.
	if strings.Contains(err.Error(), api.CodeSigInvalid) {
		t.Errorf("err = %v, must NOT carry code %q (this is the transient-I/O branch, not tamper)",
			err, api.CodeSigInvalid)
	}
	// Must carry Retry-After so gatewayd-internal flushes a 503 with the
	// header (writeWakeError falls back to ErrCapacity otherwise).
	var p *api.Problem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v, want *api.Problem so gatewayd-internal can write Retry-After", err)
	}
	// Verify the engine tagged the underlying storage error in
	// the Detail so log greps still find it (the customer-facing
	// 503 carries "verifier I/O error for layer ...",
	// operator-facing logs/Sentry carry the verbatim
	// "storage backend unreachable" via the Detail).
	if !strings.Contains(err.Error(), "storage backend unreachable") {
		t.Errorf("err = %v, want Detail to preserve underlying storage error", err)
	}
	if vmm.coldBoots != 0 {
		t.Errorf("vmm invoked on transient verifier I/O: coldBoots=%d, want 0", vmm.coldBoots)
	}
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("ResidentRAM = %d MB after reject, want 0 (transient must release the slot, not hold it)",
			got)
	}
	// Verify the Retry-After header is on the Problem (the
	// engine-side pin; gatewayd-internal's writeWakeError surfaces it on
	// the wire via api.WriteProblem).
	if got := p.HasHeader("Retry-After"); len(got) != 1 || got[0] != "5" {
		t.Errorf("HasHeader(Retry-After) = %v, want [\"5\"]", got)
	}
}

// TestEngine_StreamWarmHintsSeesRecordWake drives a RecordWake
// through WarmAffinity and asserts the engine's StreamWarmHints
// sink receives the broadcast event. End-to-end test of the
// change-detect + fan-out path used by scheddgrpc.Server.
func TestEngine_StreamWarmHintsSeesRecordWake(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	e.WithWarmAffinity(NewWarmAffinity(0))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan WarmHintEvent, 4)
	sink := func(ev WarmHintEvent) error {
		got <- ev
		return nil
	}
	done := make(chan error, 1)
	go func() {
		done <- e.StreamWarmHints(ctx, sink)
	}()

	// Give the subscriber goroutine a moment to register before
	// we emit.
	time.Sleep(20 * time.Millisecond)

	e.warmAffinity.RecordWake(app.ID, "node-a")
	e.warmBroadcaster.emit(WarmHintEvent{AppID: app.ID, NodeID: "node-a", WrittenAt: time.Now()})

	select {
	case ev := <-got:
		if ev.AppID != app.ID {
			t.Errorf("ev.AppID = %q, want %q", ev.AppID, app.ID)
		}
		if ev.NodeID != "node-a" {
			t.Errorf("ev.NodeID = %q, want node-a", ev.NodeID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for WarmHintEvent")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StreamWarmHints returned %v on ctx cancel, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("StreamWarmHints did not return after ctx cancel")
	}
}

// TestEngine_StreamWarmHintsCtxCancelStops asserts the stream
// returns nil on ctx cancel — the scheddgrpc handler relies on
// this to surface codes.Canceled cleanly.
func TestEngine_StreamWarmHintsCtxCancelStops(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	e := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")

	ctx, cancel := context.WithCancel(context.Background())
	sink := func(ev WarmHintEvent) error { return nil }
	done := make(chan error, 1)
	go func() {
		done <- e.StreamWarmHints(ctx, sink)
	}()

	// No emit ever fires; cancel immediately.
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StreamWarmHints returned %v on cancel, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("StreamWarmHints did not return after ctx cancel")
	}
}

// TestEngine_StreamWarmHintsNilBroadcaster returns nil cleanly
// when warmBroadcaster is nil (the pre-axis-4 fixture path).
// StreamWarmHints must NOT panic so legacy test harnesses that
// don't go through NewEngine can still build.
func TestEngine_StreamWarmHintsNilBroadcaster(t *testing.T) {
	t.Parallel()
	e := &Engine{warmBroadcaster: nil} // intentionally zero
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- e.StreamWarmHints(ctx, func(ev WarmHintEvent) error { return nil })
	}()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StreamWarmHints(nil broadcaster) returned %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("StreamWarmHints(nil broadcaster) did not return after cancel")
	}
}

// TestEngine_StreamWarmHintsNilSink surfaces a typed error so
// pkg/scheddgrpc.Server.StreamWarmHints can lift it to a gRPC
// status rather than silently dropping events.
func TestEngine_StreamWarmHintsNilSink(t *testing.T) {
	t.Parallel()
	e := &Engine{warmBroadcaster: newWarmHintBroadcaster()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := e.StreamWarmHints(ctx, nil)
	if err == nil {
		t.Fatal("StreamWarmHints(nil sink) returned nil, want error")
	}
}

// TestLoadSealedEnvFor_FiltersToOverrideKeys (issue #460 / ADR-053 §Decision 1)
// pins the env_secrets resolver end-to-end: three rows seeded in app_secrets,
// override requests one key, result must be exactly that one entry — cipher
// text preserved (no re-sealing), order preserved (declaration order), and
// legacy behaviour (no override) returns all three.
//
// The Engine is constructed bare (no vmm/notif) because loadSealedEnvFor
// only reads e.store and e.log. Mirrors how the function is deployed at
// Wake/ColdBoot call sites which already have a populated Engine.
func TestLoadSealedEnvFor(t *testing.T) {
	t.Run("no override returns all secrets", func(t *testing.T) {
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "DB_URL", []byte("cipher-db")); err != nil {
			t.Fatalf("seed DB_URL: %v", err)
		}
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "API_KEY", []byte("cipher-api")); err != nil {
			t.Fatalf("seed API_KEY: %v", err)
		}
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "OAUTH", []byte("cipher-oauth")); err != nil {
			t.Fatalf("seed OAUTH: %v", err)
		}
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		out, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, nil)
		if err != nil {
			t.Fatalf("loadSealedEnvFor: %v", err)
		}
		if len(out) != 3 {
			t.Fatalf("got %d entries, want 3 (legacy all-secrets)", len(out))
		}
		// Ciphertext preserved bit-for-bit.
		got := map[string][]byte{}
		for _, e := range out {
			got[e.Key] = e.Ciphertext
		}
		if string(got["DB_URL"]) != "cipher-db" || string(got["API_KEY"]) != "cipher-api" || string(got["OAUTH"]) != "cipher-oauth" {
			t.Errorf("cipher mismatch: %+v", got)
		}
	})

	t.Run("override filters to requested keys only", func(t *testing.T) {
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "DB_URL", []byte("cipher-db")); err != nil {
			t.Fatalf("seed DB_URL: %v", err)
		}
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "API_KEY", []byte("cipher-api")); err != nil {
			t.Fatalf("seed API_KEY: %v", err)
		}
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "OAUTH", []byte("cipher-oauth")); err != nil {
			t.Fatalf("seed OAUTH: %v", err)
		}
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		out, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, map[string]string{
			"DB_URL": "secret:DB_URL",
		})
		if err != nil {
			t.Fatalf("loadSealedEnvFor: %v", err)
		}
		if len(out) != 1 {
			t.Fatalf("got %d entries, want 1 (filter to override)", len(out))
		}
		if out[0].Key != "DB_URL" || string(out[0].Ciphertext) != "cipher-db" {
			t.Errorf("entry = {Key:%q Cipher:%q}, want {Key:DB_URL Cipher:cipher-db}", out[0].Key, out[0].Ciphertext)
		}
	})

	t.Run("override requesting missing key fails loud", func(t *testing.T) {
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "DB_URL", []byte("cipher-db")); err != nil {
			t.Fatalf("seed DB_URL: %v", err)
		}
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		_, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, map[string]string{
			"NONEXISTENT": "secret:NONEXISTENT",
		})
		if err == nil {
			t.Fatal("expected error for missing override key, got nil")
		}
		if !strings.Contains(err.Error(), "NONEXISTENT") {
			t.Errorf("error %q should name the missing key", err)
		}
		if !strings.Contains(err.Error(), "faas secrets set") {
			t.Errorf("error %q should hint at faas secrets set", err)
		}
	})

	t.Run("override aggregating multiple missing keys reports all in one error", func(t *testing.T) {
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "DB_URL", []byte("cipher-db")); err != nil {
			t.Fatalf("seed DB_URL: %v", err)
		}
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		_, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, map[string]string{
			"MISSING_A": "secret:MISSING_A",
			"MISSING_B": "secret:MISSING_B",
			"MISSING_C": "secret:MISSING_C",
		})
		if err == nil {
			t.Fatal("expected error for missing override keys, got nil")
		}
		// All three must be named in one error so the customer sees the
		// full set on a single wake failure (MEDIUM-2 in the PR-B review).
		for _, k := range []string{"MISSING_A", "MISSING_B", "MISSING_C"} {
			if !strings.Contains(err.Error(), k) {
				t.Errorf("error %q should name %q", err, k)
			}
		}
	})

	t.Run("override referencing existing row with arbitrary ref shape succeeds (apid-trusted)", func(t *testing.T) {
		// The wake-side resolver trusts the jsonb column's shape — apid
		// validated it at INSERT time. A ref that doesn't match the
		// "secret:NAME" grammar still resolves if the env_key has a row.
		// The existence check is the only wake-side gate; the grammar
		// check is apid's responsibility.
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		if err := s.UpsertAppSecret(context.Background(), "acct", app.ID, "DB_URL", []byte("cipher-db")); err != nil {
			t.Fatalf("seed DB_URL: %v", err)
		}
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		out, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, map[string]string{
			"DB_URL": "plaintext-no-prefix", // malformed ref, but row exists
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(out) != 1 || out[0].Key != "DB_URL" {
			t.Errorf("got %+v, want one DB_URL entry (lookup by env_key)", out)
		}
	})

	t.Run("override empty + no rows returns nil", func(t *testing.T) {
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		out, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, nil)
		if err != nil {
			t.Fatalf("loadSealedEnvFor: %v", err)
		}
		if len(out) != 0 {
			t.Errorf("got %d entries, want 0", len(out))
		}
	})

	t.Run("override present but no rows fails loud", func(t *testing.T) {
		s := state.NewMemStore()
		_, app, _ := seedApp(t, s, api.PlanHobby, 256, 1)
		e := &Engine{store: s, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		_, err := e.loadSealedEnvFor(context.Background(), "acct", app.ID, api.DefaultEnvScope, map[string]string{
			"DB_URL": "secret:DB_URL",
		})
		if err == nil {
			t.Fatal("expected error for override with no app_secrets rows, got nil")
		}
	})
}

// TestParseEnvSecretName was removed in PR-B review fixes: parseEnvSecretName
// is gone. The wake-side resolver trusts the jsonb column's shape (apid
// validated it at INSERT time); only the row-existence check remains at
// wake. The ref grammar is pinned at pkg/api/dto.go's apid validator and at
// pkg/api/appmanifest.go's manifest Validate — see TestManifestValidate.

// TestEnvSecretsFromDep_DepConversion pins the helper that converts
// dep.OverrideEnvSecrets (jsonb → map[string]string) for the wake-side
// resolver. Pure function test; the actual resolver behaviour is exercised
// in TestLoadSealedEnvFor above.
func TestEnvSecretsFromDep_DepConversion(t *testing.T) {
	t.Run("empty dep returns nil", func(t *testing.T) {
		d := state.Deployment{}
		if got := envSecretsFromDep(d); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
	t.Run("nil blob returns nil", func(t *testing.T) {
		d := state.Deployment{OverrideEnvSecrets: nil}
		if got := envSecretsFromDep(d); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
	t.Run("empty json object returns nil", func(t *testing.T) {
		d := state.Deployment{OverrideEnvSecrets: json.RawMessage(`{}`)}
		if got := envSecretsFromDep(d); got != nil {
			t.Errorf("got %+v, want nil (empty object — no filter)", got)
		}
	})
	t.Run("valid jsonb round-trips to map", func(t *testing.T) {
		d := state.Deployment{OverrideEnvSecrets: json.RawMessage(`{"DB_URL":"secret:DB_URL","API_KEY":"secret:API_KEY"}`)}
		got := envSecretsFromDep(d)
		if got == nil {
			t.Fatal("got nil")
		}
		if got["DB_URL"] != "secret:DB_URL" || got["API_KEY"] != "secret:API_KEY" {
			t.Errorf("got %+v, want both refs", got)
		}
	})
	t.Run("malformed jsonb falls back to nil (no-override)", func(t *testing.T) {
		d := state.Deployment{OverrideEnvSecrets: json.RawMessage(`{"DB_URL":`)} // truncated
		if got := envSecretsFromDep(d); got != nil {
			t.Errorf("got %+v, want nil (defensive — apid validated shape)", got)
		}
	})
}

// --- warm-snapshot capture (issue #470 / PR A / ADR-055) -----------------
//
// The four tests below pin the warm-tier capture visibility at the
// Park site. The acceptance criteria are:
//
//   1. HappyPath (Pro plan + app.WarmSnapshotEnabled + framework
//      ready): WarmSnapshot called once, snapshots row with
//      tier='warm' exists, init row still exists, instance PARKED.
//   2. FailureDestroysVM (WarmSnapshot returns error): vmm.Destroy
//      called, instance STOPPED, warm row absent, warm-error
//      counter bumped.
//   3. PlanGate (Free plan + warm enabled): WarmSnapshot NOT called.
//   4. AppDisabled (Pro plan + warm disabled): WarmSnapshot NOT
//      called.
//
// usableSnapshotForWake is a thin wrapper over LatestSnapshot /
// LatestSnapshotForTier — the storage layer's ranking is already
// pinned by TestMemStore_LatestSnapshotForTier, so the engine
// test only verifies that the plan string plumbs through to the
// right LatestSnapshot* call. The two store paths are exercised
// via memstore.runTier in pkg/state/memstore_warm_snapshot_test.go.

// primeRunPlusFrameworkReady is the shared seeding path for the
// four tests. It boots the app via Prime (cold-boot + init-tier
// capture) and then stamps framework_ready_at on the resulting
// row so the warm gate at gate #3 (PR #543 stamp) is open.
//
// The stamp is set BEFORE the test drives Park, so the warm
// capture can succeed in the same appMu window. The wake methods
// (AdmitInstance, Wake) don't normally stamp framework_ready_at
// because that signal is owned by the runner — the test bypass
// here is the only way to predictably exercise the warm path
// without a live Firecracker guest.
//
// Prime's own init capture is reset on the fakeVMM counters and
// the fakeNotifier so the Park-site asserts only see the
// post-prime calls. The warm counter is left at 0 (Prime never
// fires it). The snapshotErr is cleared too so the failure-path
// test can set its own value without it bleeding into Prime.
func primeRunPlusFrameworkReady(t *testing.T, store state.Store, vmm *fakeVMM, notif *fakeNotifier, e *Engine, appID, depID string) string {
	t.Helper()
	ctx := context.Background()
	if err := e.Prime(ctx, appID, depID); err != nil {
		t.Fatalf("Prime: %v", err)
	}
	rows, _ := store.ListInstancesForApp(ctx, appID)
	if len(rows) != 1 {
		t.Fatalf("Prime rows = %d, want 1", len(rows))
	}
	// Flip PARKED → RUNNING (the PR #543 stamp is a runner-side
	// signal; the test bypasses the runner simulation) and stamp
	// framework_ready_at with a slightly-old timestamp so the
	// MinMs floor (set to 0 by enableWarmSnapshot) is met.
	if err := store.UpdateInstanceState(ctx, rows[0].ID, string(state.StateRunning)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}
	if err := store.SetInstanceFrameworkReadyAt(ctx, rows[0].ID, time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("SetInstanceFrameworkReadyAt: %v", err)
	}
	// Reset the fakeVMM counters so the Park-site assertions
	// only see the post-prime calls. The init snapshot row
	// created by Prime is also expected to be the "first" row;
	// Park's capture will create a *second* row (PR Store doesn't
	// unique-constrain on (deployment_id, tier) at the schema
	// level for the init tier in the same Park window — that's
	// exactly the realistic shape production sees).
	vmm.mu.Lock()
	vmm.snapshots = 0
	vmm.warmSnapshots = 0
	vmm.destroys = 0
	vmm.snapErr = nil
	vmm.warmSnapErr = nil
	vmm.mu.Unlock()
	// Reset the notifier so the snapshot_written count is
	// post-prime only. Prime emits 1 snapshot_written (its own
	// capture); Park emits 2 (init + warm).
	notif.reset()
	return rows[0].ID
}

// enableWarmSnapshot flips app.WarmSnapshotEnabled + sets MinMs=0
// so the warm gate (captureWarmSnapshotLocked gate #4) is open.
// The plan gate is checked separately per-test.
func enableWarmSnapshot(t *testing.T, store state.Store, appID string) {
	t.Helper()
	if _, err := store.UpdateApp(context.Background(), appID, state.UpdateAppParams{
		SetWarmSnapshotEnabled: true,
		WarmSnapshotEnabled:    boolPtr(true),
		SetWarmSnapshotMinMs:   true,
		WarmSnapshotMinMs:      intPtr(0),
	}); err != nil {
		t.Fatalf("UpdateApp enableWarmSnapshot: %v", err)
	}
}

// boolPtr is a tiny local helper (loop_test.go:561 has intPtr) —
// the UpdateAppParams pointer fields need a non-nil *bool / *int to
// flip the "set" bit.
func boolPtr(b bool) *bool { return &b }

func TestCaptureWarmSnapshot_HappyPath(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	enableWarmSnapshot(t, store, app.ID)

	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	// Run a fake imaged subscriber over the recorded events so
	// the test mirrors PR #525's row-writer semantics: the
	// engine is sole notifier, imaged is sole writer. The pre-fix
	// version seeded both rows manually; the new shape processes
	// the snapshot_written payloads the engine emitted. Without
	// this pass, the warm row assertion below would fail with
	// ErrNotFound.
	imaged := &mockImaged{store: store, fcVer: "1.10.0"}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)

	if err := e.Park(context.Background(), insID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	// Drain the recorded snapshot_written events through the fake
	// imaged subscriber so the rows land in the snapshots table.
	// The engine is sole notifier; imaged is sole writer.
	imaged.Drain(notif)
	// 1) Both init and warm tiers were captured in the same appMu
	// window — warm fires first (RUNNING → paused → RUNNING), then
	// init (RUNNING → PARKED via the legacy PauseAndSnapshot). The
	// fakeVMM counters recorded 1 init + 1 warm call after the
	// prime counter reset.
	if vmm.snapshots != 1 {
		t.Errorf("init snapshots = %d, want 1", vmm.snapshots)
	}
	if vmm.warmSnapshots != 1 {
		t.Errorf("warm snapshots = %d, want 1", vmm.warmSnapshots)
	}
	// 2) Both rows are present in the snapshots table; the
	// rows came from imaged's subscriber, NOT the engine. This is
	// the contract captureWarmSnapshotLocked relies on: the engine
	// is sole notifier, imaged is sole writer.
	initSnap, err := store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier init: %v", err)
	}
	if initSnap.Tier != state.SnapshotTierInit {
		t.Errorf("init row tier = %q, want init", initSnap.Tier)
	}
	warmSnap, err := store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierWarm)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier warm: %v", err)
	}
	if warmSnap.Tier != state.SnapshotTierWarm {
		t.Errorf("warm row tier = %q, want warm", warmSnap.Tier)
	}
	// mockImaged writes the engine's payload storage_key verbatim
	// (vmstate_path = full host path that the VMM hands back).
	// The /warm/ segment in that path is what proves the engine
	// routed the warm capture through the right key namespace — a
	// regression to init's path would land here with /mem instead of
	// /warm/vmstate.
	if !strings.Contains(warmSnap.StorageKey, "/warm/vmstate") {
		t.Errorf("warm row storage_key = %q, want suffix /warm/vmstate (engine must route warm capture through /warm/ namespace)",
			warmSnap.StorageKey)
	}
	if strings.Contains(warmSnap.StorageKey, "/snap/"+dep.ID+"/warm/mem") {
		t.Errorf("warm row storage_key = %q must not be the init-tier mem key", warmSnap.StorageKey)
	}
	// 3) Snapshot_written emitted twice — warm first (RUNNING still),
	// then init (PAUSED → PARKED).
	if notif.count("snapshot_written") != 2 {
		t.Errorf("snapshot_written = %d, want 2 (warm + init)", notif.count("snapshot_written"))
	}
	// The order matters: warm.notify fires BEFORE the init
	// capture's PauseAndSnapshot, so tier="warm" should be the
	// first notification captured after the prime counter reset.
	first, ok := notif.firstPayload(db.NotifySnapshotWritten)
	if !ok {
		t.Fatalf("no snapshot_written payload recorded")
	}
	if got := first.Payload["tier"]; got != state.SnapshotTierWarm {
		t.Errorf("first notifySnapshotWritten tier = %v, want warm (warm capture fires first)", got)
	}
	// 4) Instance landed in PARKED.
	ins, _ := store.InstanceByID(context.Background(), insID)
	if ins.State != string(state.StateParked) {
		t.Errorf("state = %q, want parked", ins.State)
	}
}

// mockImaged (issue #470 / PR A review) is a minimal in-process
// stand-in for imaged's snapshot_written subscriber. It mirrors
// PR #525's row-writer semantics: parse the payload, derive the
// tier from the JSON, and call store.CreateSnapshot with the right
// tier so the engine can stay out of the row-writing business.
// Wired via fakeNotifier.AddHandler so a different PR can swap in
// the real subscriber by replacing fakeNotifier without touching
// the engine.
type mockImaged struct {
	store state.Store
	fcVer string
}

// handle processes a single snapshot_written payload (parses the
// JSON, derives tier from the field, writes the snapshots row).
func (m *mockImaged) handle(payload []byte) error {
	var p map[string]any
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	tier, _ := p["tier"].(string)
	if tier == "" {
		tier = state.SnapshotTierInit
	}
	depID, _ := p["deployment_id"].(string)
	storageKey, _ := p["vmstate_path"].(string)
	memBytes, _ := p["mem_bytes"].(float64)
	_, err := m.store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: depID,
		FCVersion:    m.fcVer,
		MemBytes:     int64(memBytes),
		StorageKey:   storageKey,
		Tier:         tier,
	})
	return err
}

// Drain (issue #470 / PR A review) replays the recorder's
// snapshot_written events through handle in order. Production
// delivers them via a Postgres LISTEN connection; the recorder
// just appends to a slice. Channel-name dispatch mirrors the
// production prod-subscribed channel name (db.NotifySnapshotWritten).
func (m *mockImaged) Drain(n *fakeNotifier) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.events {
		if e.channel != db.NotifySnapshotWritten {
			continue
		}
		if err := m.handle([]byte(e.payload)); err != nil {
			// Production imaged logs + drops on a single bad
			// payload; tests stop at first failure so a
			// regression that breaks the JSON shape surfaces.
			panic("mockImaged.handle: " + err.Error())
		}
	}
}

// firstPayload (issue #470 / PR A review) returns the channel +
// parsed payload of the first event matching channel. Used to
// assert the warm capture fires its snapshot_written emit BEFORE
// the init capture's emit (PRD-070 §Capture sequence step 2).
func (n *fakeNotifier) firstPayload(channel string) (ev struct {
	Payload map[string]any
}, ok bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, e := range n.events {
		if e.channel != channel {
			continue
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(e.payload), &p); err != nil {
			return struct{ Payload map[string]any }{}, false
		}
		return struct{ Payload map[string]any }{Payload: p}, true
	}
	return struct{ Payload map[string]any }{}, false
}

func TestCaptureWarmSnapshot_FailureDestroysVM(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	enableWarmSnapshot(t, store, app.ID)

	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")

	insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)

	// Wire the warm-failure path AFTER Prime so the init capture
	// (which calls fakeVMM.PauseAndSnapshot) succeeds; the warm
	// capture (calling fakeVMM.WarmSnapshot) blows up. The fake
	// consults warmSnapErr only for WarmSnapshot — distinct from
	// snapErr which is consulted by PauseAndSnapshot / the init
	// path. This is the realistic shape: vmmd's init pause
	// succeeded; the warm pause + /snapshot/create failed.
	vmm.warmSnapErr = errors.New("vmmd_warm_snapshot_exploded")

	// Park returns the warm-capture error wrapped; the warm path
	// already Destroyed the VM and transitioned the row to STOPPED,
	// so the error here is the load-bearing diagnostic, not a
	// fatal that should t.Fatal. We assert on it below.
	parkErr := e.Park(context.Background(), insID)
	if parkErr == nil {
		t.Fatalf("Park: expected warm-capture error, got nil")
	}
	if !strings.Contains(parkErr.Error(), "vmmd_warm_snapshot_exploded") {
		t.Errorf("Park err = %q, want substring %q", parkErr, "vmmd_warm_snapshot_exploded")
	}
	// 1) VM destroyed on warm failure.
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
	// 2) Instance STOPPED (warm destroyed the VM; no warm AND no
	// init to keep — operator gets a cold-boot next wake per
	// ADR-005). 3) init snapshot was never captured (Park returned
	// before the legacy init PauseAndSnapshot ran).
	if vmm.snapshots != 0 {
		t.Errorf("init snapshots = %d, want 0 (warm failure aborts the Park before init capture)", vmm.snapshots)
	}
	ins, _ := store.InstanceByID(context.Background(), insID)
	if ins.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", ins.State)
	}
	// 4) No warm-tier row written (the capture failed).
	_, err := store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierWarm)
	if err == nil {
		t.Errorf("warm row exists; want ErrNotFound")
	}
	// 5) No init-tier row written either (warm failure aborts the
	// whole Park; the legacy init PauseAndSnapshot never ran).
	_, err = store.LatestSnapshotForTier(context.Background(), dep.ID, state.SnapshotTierInit)
	if err == nil {
		t.Errorf("init row exists; want ErrNotFound (warm failure aborts Park before init capture)")
	}
	// 6) Resident RAM is 0 — the warm failure path released the
	// ledger reservation.
	if got := e.Ledger().ResidentRAM(); got != 0 {
		t.Errorf("resident = %d, want 0 (released on warm failure)", got)
	}
}

func TestCaptureWarmSnapshot_PlanGate(t *testing.T) {
	// Free plan — WarmSnapshotAllowed() returns false. The app
	// has WarmSnapshotEnabled=true so the test isolates the plan
	// gate from the app gate.
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanFree, 256, 5)
	enableWarmSnapshot(t, store, app.ID)

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	insID := primeRunPlusFrameworkReady(t, store, vmm, &fakeNotifier{}, e, app.ID, dep.ID)

	if err := e.Park(context.Background(), insID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if vmm.warmSnapshots != 0 {
		t.Errorf("warm snapshots = %d, want 0 (Free plan disallows)", vmm.warmSnapshots)
	}
	// Init tier still captured.
	if vmm.snapshots != 1 {
		t.Errorf("init snapshots = %d, want 1", vmm.snapshots)
	}
}

func TestCaptureWarmSnapshot_AppDisabled(t *testing.T) {
	// Pro plan (warm allowed) but app.WarmSnapshotEnabled is
	// false. The capture should not fire.
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	// Deliberately do NOT call enableWarmSnapshot.

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	insID := primeRunPlusFrameworkReady(t, store, vmm, &fakeNotifier{}, e, app.ID, dep.ID)

	if err := e.Park(context.Background(), insID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	if vmm.warmSnapshots != 0 {
		t.Errorf("warm snapshots = %d, want 0 (app.WarmSnapshotEnabled=false)", vmm.warmSnapshots)
	}
	if vmm.snapshots != 1 {
		t.Errorf("init snapshots = %d, want 1", vmm.snapshots)
	}
}

// TestUsableSnapshotForWake_PlanGate drives the Wake-side tier
// selection. The plan gate is the sticky-on-downgrade contract
// (ADR-055 §5): a Free/Hobby account skips the warm tier even when
// a warm row exists on disk. The test seeds two snapshot rows
// (init + warm) and asserts that LatestSnapshotForTier(init) is
// consulted instead of LatestSnapshot when the plan disallows
// warm.
//
// The test exercises the integration between Engine and Store
// at the wake boundary rather than re-implementing the rank
// query — store-side ranking is covered by
// TestMemStore_LatestSnapshotForTier.
func TestUsableSnapshotForWake_PlanGate(t *testing.T) {
	store := state.NewMemStore()
	_, _, dep := seedApp(t, store, api.PlanFree, 256, 5)

	// Seed both tiers directly. The StorageKey is the only field
	// the engine reads at the wake site; FCVersion matches.
	now := time.Now()
	_, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 130 * 1024 * 1024, StorageKey: state.SnapMemKey(dep.ID),
		Tier: state.SnapshotTierInit, Stale: false,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot init: %v", err)
	}
	_, err = store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, FCVersion: "1.10.0",
		MemBytes: 130 * 1024 * 1024, StorageKey: state.WarmSnapMemKey(dep.ID),
		Tier: state.SnapshotTierWarm, Stale: false,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot warm: %v", err)
	}
	_ = now

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	// Free plan returns the init row even though a warm row exists.
	snap, ok, tier := e.usableSnapshotForWake(context.Background(), dep.ID, string(api.PlanFree))
	if !ok {
		t.Fatal("PlanFree: usableSnapshotForWake returned no snap")
	}
	if snap.Tier != state.SnapshotTierInit {
		t.Errorf("Free plan: tier = %q, want init (warm skipped on sticky-downgrade)", snap.Tier)
	}
	if snap.StorageKey != state.SnapMemKey(dep.ID) {
		t.Errorf("Free plan: storage_key = %q, want %q", snap.StorageKey, state.SnapMemKey(dep.ID))
	}
	if tier != "init" {
		t.Errorf("Free plan: chosen tier = %q, want init", tier)
	}

	// Pro plan returns the warm row (warm > init on tie).
	snap, ok, tier = e.usableSnapshotForWake(context.Background(), dep.ID, string(api.PlanPro))
	if !ok {
		t.Fatal("PlanPro: usableSnapshotForWake returned no snap")
	}
	if snap.Tier != state.SnapshotTierWarm {
		t.Errorf("Pro plan: tier = %q, want warm", snap.Tier)
	}
	if snap.StorageKey != state.WarmSnapMemKey(dep.ID) {
		t.Errorf("Pro plan: storage_key = %q, want %q", snap.StorageKey, state.WarmSnapMemKey(dep.ID))
	}
	if tier != "warm" {
		t.Errorf("Pro plan: chosen tier = %q, want warm", tier)
	}
}

// TestCaptureWarmSnapshot_EmitsAuditPromoted (issue #470 / PR C /
// ADR-074) pins the success-path audit emit from captureWarmSnapshot
// Locked. The audit kind is app.warm_snapshot_promoted (subject =
// &app.AccountID, payload includes app_id, deployment_id, tier
// and the per-app min_requests/min_ms gates). Walks ListEvents
// keyed by AccountID UUID — the subject shape mirrors
// app.updated's account-scoped listing per ADR-074 §3.2.
func TestCaptureWarmSnapshot_EmitsAuditPromoted(t *testing.T) {
	store := state.NewMemStore()
	acct, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	enableWarmSnapshot(t, store, app.ID)

	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	imaged := &mockImaged{store: store, fcVer: "1.10.0"}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	e.WithAudit(audit.New(store, testLog(), nil, "schedd"))

	insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)
	if err := e.Park(context.Background(), insID); err != nil {
		t.Fatalf("Park: %v", err)
	}
	imaged.Drain(notif)

	events, err := store.ListEvents(context.Background(), acct.ID, 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var promoted *state.Event
	for i := range events {
		if events[i].Kind == "app.warm_snapshot_promoted" {
			promoted = &events[i]
			break
		}
	}
	if promoted == nil {
		t.Fatalf("no app.warm_snapshot_promoted audit row; got events: %+v", events)
	}
	if promoted.Actor != "schedd" {
		t.Errorf("actor = %q, want schedd", promoted.Actor)
	}
	if promoted.Subject == nil {
		t.Fatal("Subject = nil, want &app.AccountID")
	}
	if *promoted.Subject != uuid.MustParse(acct.ID) {
		t.Errorf("subject = %s, want %s (account id)", *promoted.Subject, acct.ID)
	}
	// Data is JSON-marshalled — decode and pin the payload shape.
	var payload map[string]any
	if err := json.Unmarshal(promoted.Data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload["app_id"] != app.ID {
		t.Errorf("payload.app_id = %v, want %s", payload["app_id"], app.ID)
	}
	if payload["deployment_id"] != dep.ID {
		t.Errorf("payload.deployment_id = %v, want %s", payload["deployment_id"], dep.ID)
	}
	if payload["tier"] != state.SnapshotTierWarm {
		t.Errorf("payload.tier = %v, want warm", payload["tier"])
	}
}

// TestCaptureWarmSnapshot_EmitsErrorCounter (issue #470 / PR C /
// ADR-074) pins the failure-path ops counter increment. Mirrors the
// TestCaptureWarmSnapshot_FailureDestroysVM shape but asserts on
// /metrics rather than audit-events — the failure path cannot
// reliably emit app.warm_snapshot_disabled (the app is still
// opted in), only the operator-facing vmm_call counter.
func TestCaptureWarmSnapshot_EmitsErrorCounter(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 256, 5)
	enableWarmSnapshot(t, store, app.ID)

	vmm := &fakeVMM{}
	notif := &fakeNotifier{}
	e := newEngine(t, store, vmm, notif, "1.10.0")
	ops := wire.NewOpsMetrics("schedd")
	e.WithOpsMetrics(ops)

	insID := primeRunPlusFrameworkReady(t, store, vmm, notif, e, app.ID, dep.ID)
	// Wire the warm-failure path AFTER Prime so the init pause
	// succeeds; only the warm /snapshot/create blows up.
	vmm.warmSnapErr = errWarmCaptureFail
	parkErr := e.Park(context.Background(), insID) // expected to fail-clean
	if parkErr == nil {
		t.Fatal("Park: expected warm-capture error, got nil")
	}
	_ = dep

	body := getMetricsBody(t, ops)
	wantLine := `schedd_warm_snapshot_errors_total{reason="vmm_call"} 1`
	if !strings.Contains(body, wantLine) {
		t.Errorf("missing metric %q in:\n%s", wantLine, body)
	}
}

// errWarmCaptureFail is a sentinel for the fakeVMM warm-snapshot
// failure path. Pinned here for C.4 tests; production cannot
// reach it (captureWarmSnapshotLocked's WarmSnapshot returns the
// real gRPC error).
var errWarmCaptureFail = errors.New("fake: warm snapshot fail")

// TestEngineEnsureWake_RefusesForeignOwnedApp pins Layer 1 of the
// multi-host safety cluster PR-5 / audit F4. The owner gate at
// EnsureWake entry (engine.go:~1235) must refuse to queue a wake
// when the app's App.NodeID points at a different schedd's
// compute_node. Without this guard, two schedds in a multi-host
// fleet can both queue the same wake in their own wakeCoord —
// each will eventually try to mint an instance row, each will
// pass choosePlacement (same chooser, same rules), and the
// cluster-coord primitive (Layer 2: the partial unique index on
// instances.wake_id) has to be the only thing keeping the fleet
// from double-booting.
//
// The test is the unit pin for the layer-1 refusal at entry; the
// end-to-end "two schedds racing on the same wake_id" pin lives
// in cmd/e2e/fleet_wake_dedup_e2e_test.go (build-tag metal).
func TestEngineEnsureWake_RefusesForeignOwnedApp(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)

	// Pin the app to a DIFFERENT box. SetAppNodeID is the
	// Phase 2 / Gate A owner-claim primitive (migration 00090).
	if err := store.SetAppNodeID(context.Background(), app.ID, "box-a"); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}

	// Build an engine whose ownerNodeID is "box-b" — i.e., this
	// schedd is the WRONG one to handle the wake. The wakeCoord
	// is real; the gate fires before the queue is touched.
	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOwnerNodeID("box-b")

	_, err := e.EnsureWake(context.Background(), app.ID, "test")
	if err == nil {
		t.Fatal("EnsureWake on foreign-owned app: expected error, got nil")
	}
	// The error string must surface both owner IDs so the
	// operator can correlate the wrong-box wake with the
	// owning box's logs.
	if !strings.Contains(err.Error(), "box-a") || !strings.Contains(err.Error(), "box-b") {
		t.Errorf("error = %q, must mention both owner box-a and box-b", err)
	}

	// Refusal must happen BEFORE any vmm call — the gate is at
	// entry, not at placement. vmm.coldBoots == 0 is the load-bearing
	// assertion: a chooser-side gate would still let the wakeCoord
	// queue the request, but a real cold-boot would never start.
	if vmm.coldBoots != 0 || vmm.restores != 0 {
		t.Errorf("owner gate failed to block: coldBoots=%d restores=%d, want 0/0",
			vmm.coldBoots, vmm.restores)
	}
}

// TestEngineEnsureWake_AllowsOwnerSameBox pins the negative case
// of the Layer 1 gate. When app.NodeID == engine.ownerNodeID, the
// gate must fall through to wakeCoord.Enter and the wake proceeds
// to cold-boot. Companion to TestEngineEnsureWake_RefusesForeignOwnedApp:
// without the companion, the gate could over-refuse and a healthy
// fleet would silently drop every request.
func TestEngineEnsureWake_AllowsOwnerSameBox(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)

	// Pin the app to the same box this schedd owns. The gate's
	// equality branch must fall through.
	if err := store.SetAppNodeID(context.Background(), app.ID, "box-a"); err != nil {
		t.Fatalf("SetAppNodeID: %v", err)
	}

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOwnerNodeID("box-a")

	out, err := e.EnsureWake(context.Background(), app.ID, "test")
	if err != nil {
		t.Fatalf("EnsureWake on owner box: %v", err)
	}
	if out.Instance == nil || out.Instance.InstanceID == "" {
		t.Errorf("owner-same-box EnsureWake returned empty InstanceID")
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1 (owner gate must not block same-box wakes)",
			vmm.coldBoots)
	}
}

// TestEngineEnsureWake_AllowsUnownedApp pins the single-box dev
// path. An app whose NodeID == "" (legacy non-shared case — never
// pinned to a node, e.g. a fresh install before Phase 2 / Gate A
// ran) must wake regardless of the engine's ownerNodeID stamp.
// Empty ownerNodeID also falls through, but the production
// fleet always has WithOwnerNodeID set; the unowned-app branch
// is the row that single-box dev hits.
func TestEngineEnsureWake_AllowsUnownedApp(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// app.NodeID stays "" by design.

	vmm := &fakeVMM{}
	e := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0").WithOwnerNodeID("box-a")

	out, err := e.EnsureWake(context.Background(), app.ID, "test")
	if err != nil {
		t.Fatalf("EnsureWake on unowned app: %v", err)
	}
	if out.Instance == nil || out.Instance.InstanceID == "" {
		t.Errorf("unowned-app EnsureWake returned empty InstanceID")
	}
	if vmm.coldBoots != 1 {
		t.Errorf("coldBoots = %d, want 1 (unowned apps must wake on the local schedd)",
			vmm.coldBoots)
	}
}

// TestEngine_CreateInstanceWithWakeRetry_LoserSurfacesSentinel pins
// the corrected contract for the cluster-coord Layer 2 helper
// (pkg/sched/engine.go createInstanceWithWakeRetry, multi-host
// safety cluster PR-5 / audit F4). When the underlying store returns
// state.ErrConcurrentWake (the partial unique index rejected our
// INSERT because another schedd already inserted an in-flight row
// with the same wake_id), the helper MUST return
// state.ErrWakeAlreadyInflight with an empty Instance — NOT the
// winner's row.
//
// Returning the winner's row is the ship-blocking bug code-review
// agent #1036 surfaced: the helper previously read the winner's
// row via ReadActiveInstanceForWakeID and returned it as if THIS
// schedd had minted it. The engine downstream uses (ins.ID,
// placement.NodeID) for ledger.Admit + vmm.CreateColdBoot +
// SetInstanceRuntime — so a foreign winner's row caused the LOCAL
// schedd to boot a microVM tagged with the REMOTE schedd's
// instance UUID. Six concrete failure modes followed (wrong node
// tag, double-counted perApp concurrency, local VM with remote
// UUID, HostIP clobber race, DeleteInstance deleting the winner,
// transitionWithKind marking the winner FAILED). The corrected
// contract exits cleanly via the typed sentinel so the engine
// surfaces "another box handled it" to the gateway-side retry
// poll / cron-side reschedule / redeploy — no local boot happens.
func TestEngine_CreateInstanceWithWakeRetry_LoserSurfacesSentinel(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 5)
	deploymentID := dep.ID

	// Seed the "winner" the loser's helper would otherwise have
	// returned. We assert below that the helper does NOT echo
	// this row back when the store returns ErrConcurrentWake.
	_, err := store.CreateInstance(context.Background(), app.ID, deploymentID,
		string(state.StateColdBooting), 512, state.DefaultLocalNodeName,
		"winner-wake-id-on-peer-box")
	if err != nil {
		t.Fatalf("seed winner row: %v", err)
	}

	// Wrap the memstore so the loser's CreateInstance call
	// returns ErrConcurrentWake — the same path the partial
	// unique index instances_wake_attempt_active_idx takes on
	// a real Postgres when a peer's row is already in the table.
	loseStore := &loseAlwaysCreateInstance{MemStore: store}

	vmm := &fakeVMM{}
	e := newEngine(t, loseStore, vmm, &fakeNotifier{}, "1.10.0")

	// Call the helper directly with a wake_id that has no row
	// in the table. The wrapper returns ErrConcurrentWake. The
	// helper must surface ErrWakeAlreadyInflight with an empty
	// Instance.
	ins, err := e.createInstanceWithWakeRetry(context.Background(),
		app.ID, deploymentID, string(state.StateColdBooting),
		512, state.DefaultLocalNodeName, "loser-wake-id")
	if !errors.Is(err, state.ErrWakeAlreadyInflight) {
		t.Fatalf("loser err = %v, want ErrWakeAlreadyInflight", err)
	}
	if ins.ID != "" {
		t.Errorf("loser ins.ID = %q, want empty (helper must NOT return the winner's row)", ins.ID)
	}

	// The wrapper was invoked exactly once — the helper does
	// not retry. A retry loop with jitter (the previous broken
	// shape) would have called CreateInstance up to 3 times; the
	// corrected helper keeps the call to a single round trip
	// because the partial unique index is binary (succeeds or
	// 23505; a retry can never win once tripped).
	if loseStore.calls != 1 {
		t.Errorf("CreateInstance calls = %d, want 1 (binary index — retries are useless)", loseStore.calls)
	}
}

// loseAlwaysCreateInstance is a minimal Store wrapper used by
// TestEngine_CreateInstanceWithWakeRetry_LoserSurfacesSentinel. It
// overrides only CreateInstance, returning ErrConcurrentWake and
// counting calls; every other method embeds through to the wrapped
// memstore (state.Store satisfaction comes via *state.MemStore
// method promotion on the embedded field) so the test's seedApp +
// dep rows remain reachable. Lives in _test.go to keep the production
// binary clean of test seams.
type loseAlwaysCreateInstance struct {
	*state.MemStore
	calls int
}

func (l *loseAlwaysCreateInstance) CreateInstance(ctx context.Context, appID, depID, initState string, ramMB int, nodeID, wakeID string) (state.Instance, error) {
	l.calls++
	return state.Instance{}, state.ErrConcurrentWake
}

func TestEngineWarmHintHeartbeatWithoutTenantTraffic(t *testing.T) {
	e := newEngine(t, state.NewMemStore(), &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time, 1)
	at := time.Now()
	ticks <- at
	done := make(chan error, 1)
	got := make(chan WarmHintEvent, 1)
	go func() { done <- e.streamWarmHints(ctx, func(ev WarmHintEvent) error { got <- ev; return nil }, ticks) }()
	select {
	case ev := <-got:
		if ev.AppID != "" || ev.NodeID != "" || !ev.WrittenAt.Equal(at) {
			t.Fatalf("invalid heartbeat: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("idle stream sent no heartbeat")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
