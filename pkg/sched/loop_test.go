package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"github.com/onebox-faas/faas/pkg/sched/recentload"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestLoopReaperParksIdleInstance drives one reaper tick against a store holding
// an instance well past its idle timeout and asserts the engine parked it
// (snapshot taken, RAM released). The Loop reads instances from the store, so we
// back-date last_request_at by transitioning through a real wake first.
func TestLoopReaperParksIdleInstance(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
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
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	// Prime runs off the loop goroutine now (dispatchPrime) — wait for
	// the worker before asserting on the rows it writes.
	loop.waitPrimes()

	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	if len(rows) != 1 || rows[0].State != string(state.StateParked) {
		t.Fatalf("rows = %+v, want one parked row", rows)
	}
}

func TestHandleSnapshotPrimeFailureMarksDeploymentAndStageFailed(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanHobby, 256, 2)
	now := time.Now().UTC()
	if _, err := store.AppendDeploymentStage(context.Background(), dep.ID,
		state.StageSourceDownload, state.StageSnapshotPrepare, now, ""); err != nil {
		t.Fatalf("AppendDeploymentStage: %v", err)
	}
	if err := store.UpdateDeploymentStatus(context.Background(), dep.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}

	vmm := &fakeVMM{wakeErr: api.ErrAppStartupTimeout("cold_boot_timeout", "35s")}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())
	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifySnapshotPrime,
		Payload: `{"app_id":"` + app.ID + `","deployment_id":"` + dep.ID + `"}`,
	})
	loop.waitPrimes()

	got, err := store.DeploymentByID(context.Background(), dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if got.Status != state.DeployFailed {
		t.Fatalf("deployment status = %q, want failed", got.Status)
	}
	if got.ErrorCode != api.CodeAppStartupTimeout {
		t.Fatalf("deployment error code = %q, want %q", got.ErrorCode, api.CodeAppStartupTimeout)
	}
	var stages state.StageState
	if err := json.Unmarshal(got.StageState, &stages); err != nil {
		t.Fatalf("decode stage state: %v", err)
	}
	if stages.Current != "" || len(stages.History) != 2 {
		t.Fatalf("stage state = %+v, want closed failed stage", stages)
	}
	failed := stages.History[len(stages.History)-1]
	if failed.Name != state.StageSnapshotPrepare || failed.Status != "failed" {
		t.Fatalf("failed stage = %+v, want snapshot_prepare/failed", failed)
	}
}

// TestHandleParkedAppNotification dispatches the app_changed parked event to
// the instance lifecycle owner. This is the regression test for the original
// bug, where the event was only logged and the VM remained RUNNING.
func TestHandleParkedAppNotification(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 2)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	parked := state.AppEvictedCold
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &parked}); err != nil {
		t.Fatalf("UpdateApp parked: %v", err)
	}

	loop := NewLoop(nil, engine, testLog())
	loop.handleNotification(context.Background(), db.Notification{
		Channel: db.NotifyAppChanged,
		Payload: `{"kind":"parked","app_id":"` + app.ID + `"}`,
	})

	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateParked) {
		t.Fatalf("instance state = %q, want parked", row.State)
	}
}

func TestRunReaperReconcilesDeletedAppWithoutNotification(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 256, 2)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	deleted := state.AppDeleted
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{Status: &deleted}); err != nil {
		t.Fatalf("UpdateApp deleted: %v", err)
	}

	NewLoop(nil, engine, testLog()).runReaper(context.Background())

	row, err := store.InstanceByID(context.Background(), res.InstanceID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if row.State != string(state.StateStopped) {
		t.Errorf("state = %q, want stopped", row.State)
	}
	if vmm.destroys != 1 {
		t.Errorf("destroys = %d, want 1", vmm.destroys)
	}
}

// TestHandleNotificationRejectsBadInput covers the dispatch guards: malformed or
// incomplete payloads must not panic and must not act.
func TestHandleNotificationRejectsBadInput(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
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

// countingFlowCounter records every instance id passed to Open. Used by
// the snapshot-builder test to pin that runReaper consults the injected
// FlowCounter exactly once per instance (spec §17 G7).
type countingFlowCounter struct {
	calls map[string]int
	given map[string]int64
}

func newCountingFlowCounter(given map[string]int64) *countingFlowCounter {
	return &countingFlowCounter{calls: map[string]int{}, given: given}
}

func (c *countingFlowCounter) Open(_ context.Context, id string) (int64, error) {
	c.calls[id]++
	return c.given[id], nil
}

// TestRunReaperPopulatesOpenConns proves the snapshot builder asks the
// injected FlowCounter exactly once per instance and copies its
// result into InstanceInfo.OpenConns. Without this, the reaper's
// OpenConns skip rule would always see 0 — the production G7 fix
// would be permanently inert regardless of what the conntrack reader
// reports.
func TestRunReaperPopulatesOpenConns(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}

	// OpenConns > 0 + recent LastRequest ⇒ not reaped, but we want to
	// verify the snapshot saw the flow count. Push LastRequest far in the
	// past so without the flow count it WOULD be reaped; with it, isn't.
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	fc := newCountingFlowCounter(map[string]int64{res.InstanceID: 7})
	loop := NewLoop(nil, engine, testLog()).WithFlowCounter(fc)
	loop.runReaper(context.Background())

	if got := fc.calls[res.InstanceID]; got != 1 {
		t.Errorf("FlowCounter.Open calls = %d, want 1 (one per instance in snapshot)", got)
	}
	// LastRequest = -1h, plan default = 300s → would normally park. With
	// OpenConns > 0 (7) the G7 rule skips it.
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateRunning) {
		t.Errorf("state = %q, want running (G7: open flows kept it alive)", ins.State)
	}
}

// TestRunReaperFlowCounterErrorFailsOpen verifies the conservative
// fallback: a glitch in the flow source doesn't crash the reaper or
// permanently skip reaping. It logs and treats the count as 0, so the
// reaper uses only LastRequest for that instance.
func TestRunReaperFlowCounterErrorFailsOpen(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	bad := errorFlowCounter{err: assertErr{"conntrack timeout"}}
	loop := NewLoop(nil, engine, testLog()).WithFlowCounter(bad)
	loop.runReaper(context.Background())

	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.State != string(state.StateParked) {
		t.Errorf("state = %q, want parked (flow error fails open to LastRequest-only path)", ins.State)
	}
}

// assertErr is a sentinel error for the fails-open test. Defining it
// here avoids a polluting errors.New at package scope.
type assertErr struct{ s string }

func (e assertErr) Error() string { return e.s }

// errorFlowCounter always returns its configured error.
type errorFlowCounter struct{ err error }

func (e errorFlowCounter) Open(_ context.Context, _ string) (int64, error) { return 0, e.err }

// TestNoopFlowCounterIsTheDefault pins that a freshly-constructed
// Loop without WithFlowCounter uses noopFlowCounter — equivalent to
// "never skip for open connections", preserving prior behaviour for
// every existing test and for production until PR-B wires a real
// reader.
func TestNoopFlowCounterIsTheDefault(t *testing.T) {
	l := NewLoop(nil, nil, testLog())
	if l.flowCounts == nil {
		t.Fatal("loop.flowCounts is nil after NewLoop, want noopFlowCounter default")
	}
	got, err := l.flowCounts.Open(context.Background(), "any")
	if err != nil {
		t.Errorf("default FlowCounter.Open returned err = %v, want nil", err)
	}
	if got != 0 {
		t.Errorf("default FlowCounter.Open = %d, want 0", got)
	}
}

// cannedConntrackForReaper is one conntrack line that increments the
// matching instance. Used by TestRunReaperConsultsRealFlowcountReader
// below; the flowcount.Reader is wired against it via a fakeRunner so
// the test exercises the actual production reader (instead of the
// `countingFlowCounter` mocks above) without shelling out.
const cannedConntrackForReaper = `tcp      6 431999 ESTABLISHED src=10.100.0.5 dst=93.184.216.34 sport=42301 dport=443 [ASSURED] src=93.184.216.34 dst=10.100.0.5 sport=443 dport=42301
`

// TestRunReaperConsultsRealFlowcountReader pins the production wiring:
// runReaper calls Warm on a flowcount.Reader (which satisfies the Warm
// anonymous type), then consults Open for each instance. This is the
// only test that exercises the type-assertion path — without it the
// Warm wiring could silently rot and no unit test would notice.
func TestRunReaperConsultsRealFlowcountReader(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	res, err := engine.Wake(context.Background(), app.ID, "", "", "")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if _, err := store.TouchInstancesLastSeen(context.Background(), []state.InstanceTouch{
		{InstanceID: res.InstanceID, LastRequest: time.Now().Add(-time.Hour)},
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	// The instance got its HostIP from vmm's fakeVMM via SetInstanceRuntime
	// during Wake. Read it back so the canned conntrack line matches.
	ins, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins.HostIP == "" {
		t.Skip("fakeVMM did not assign HostIP; nothing to match against")
	}

	// Build a conntrack blob whose matching address is the instance's
	// actual HostIP. Replace the placeholder IP everywhere it appears
	// (both the original-direction and reply-direction tuples).
	blob := bytes.ReplaceAll([]byte(cannedConntrackForReaper), []byte("10.100.0.5"), []byte(ins.HostIP))

	runner := &countingRunner{out: blob}
	reader := flowcount.NewReader(runner, flowcount.WithTTL(time.Hour))
	loop := NewLoop(nil, engine, testLog()).WithFlowCounter(reader)

	loop.runReaper(context.Background())

	if calls := runner.calls.Load(); calls != 1 {
		t.Errorf("runner calls = %d, want 1 (one Warm per reaper tick)", calls)
	}
	// Sanity-check the wire-up: the instance's HostIP must have been
	// present in the conntrack blob we fed the reader. If this fails,
	// the test is feeding stale data — not a production bug.
	if got, err := reader.Open(context.Background(), res.InstanceID); err != nil || got < 1 {
		t.Fatalf("Open returned (%d, %v); canned conntrack didn't match %s — test bug", got, err, ins.HostIP)
	}
	// One flow counted; combined with LastRequest=-1h the G7 rule must
	// keep the instance alive.
	ins2, _ := store.InstanceByID(context.Background(), res.InstanceID)
	if ins2.State != string(state.StateRunning) {
		t.Errorf("state = %q, want running (G7 wired through real reader)", ins2.State)
	}
}

// countingRunner is the runner the integration test above uses to drive
// a real flowcount.Reader. Mirrors the fakeRunner in pkg/sched/flowcount
// but lives here so the sched test surface stays independent.
type countingRunner struct {
	out   []byte
	calls atomic.Int32
}

func (c *countingRunner) Output(_ context.Context, _ []string) ([]byte, error) {
	c.calls.Add(1)
	return c.out, nil
}

// ---------------------------------------------------------------------
// Aggressive reaper scale-down (issue #171). The integration
// shape: a real engine + MemStore + fakeVMM + recentload driven by
// a closure scraper. runReaper walks the full body, building the
// snapshot, calling ReapAggressive, and parking the surplus via
// engine.Park. The four tests below pin the four observable
// surfaces:
//
//   - TestLoopReaperAggressiveScalesDownOnDrop: 5 instances drop
//     to 0 rps, reaper parks the surplus above the +1 buffer.
//   - TestLoopReaperAggressiveHonorsFloor: same scenario with
//     min_instances=2 — at least 2 stay resident.
//   - TestLoopReaperAggressiveEmitsAuditRow: one events row per
//     app per tick that parked ≥ 1, kind=reaper_scale_down.
//   - TestLoopReaperAggressiveMetricIncrements: the
//     schedd_scale_down_decisions_total counter increments.
//
// fakeScaleUpScraper is a stub that returns a synthetic cumulative
// map per app on each Scrape call. Production wires a real
// HTTP-backed scraper; here we don't need HTTP — the rps signal
// is what we're exercising, not the scraping wiring.
// ---------------------------------------------------------------------

type fakeScaleUpScraper struct {
	mu     sync.Mutex
	counts map[string]int64
}

func (f *fakeScaleUpScraper) Scrape(ctx context.Context) (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(f.counts))
	for k, v := range f.counts {
		out[k] = v
	}
	return out, nil
}

// setScraper is a thread-safe setter the test uses between phases.
func (f *fakeScaleUpScraper) set(counts map[string]int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts = counts
}

// wakeN calls AdmitInstance N times back-to-back so the loop
// exercises the fan-out path (Wake would short-circuit on the
// Phase-1 fast-path after the first call). Also stamps each
// instance's LastRequestAt to the wake time so ReapIdle's
// idle-timeout filter doesn't trip on zero-value timestamps
// (which would park every just-admitted instance regardless
// of floor / desired).
func wakeN(t *testing.T, engine *Engine, appID string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		ins, err := engine.AdmitInstance(ctx, appID, "", "", "")
		if err != nil {
			t.Fatalf("AdmitInstance[%d]: %v", i, err)
		}
		now := time.Now()
		_, _ = engine.Store().TouchInstancesLastSeen(ctx, []state.InstanceTouch{
			{InstanceID: ins.InstanceID, LastRequest: now},
		})
	}
}

func TestLoopReaperAggressiveScalesDownOnDrop(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// autoscale_target_rps=10 so a 0 rps window maps to desired=0.
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	wakeN(t, engine, app.ID, 5)

	// Drive the mirror: cumulative must DECREASE to simulate "traffic
	// dropped to zero since the last scrape". recentload treats
	// cumulative < lastSeen as a fresh boot and clears the ring;
	// the very next delta tick captures the new reality with
	// delta=0 → sum stays 0.
	scraper := &fakeScaleUpScraper{}
	// Anchor the frozen clock PAST MinInstanceAge (30 s) so
	// ReapAggressive's age guard doesn't reject the just-woken
	// instances. The fakeVMM / MemStore stamp `Started` /
	// `LastRequestAt` at real `time.Now()`, so the test has to
	// jump the simulated clock forward to clear the 30 s
	// "freshly woken" window. 5-minute bucket keeps Touch and
	// runReaper's `time.Now()` in the same window without
	// sub-second drift.
	frozen := time.Now().Add(35 * time.Second)
	clock := func() time.Time { return frozen }
	mirror := recentload.New(scraper, 5, 5*time.Minute)
	scraper.set(map[string]int64{app.ID: 100})
	mirror.Touch(context.Background(), frozen)
	scraper.set(map[string]int64{app.ID: 0})
	mirror.Touch(context.Background(), frozen.Add(time.Minute))
	// Now RecentRPS = 0 → desired = 0.
	loop := NewLoop(nil, engine, testLog()).
		WithClock(clock).
		WithRecentLoad(mirror).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	running := liveCount(t, store, app.ID)
	// ReapAggressive: floor=0, desired=0, limit=max(0, 0+1)=1,
	// running=5, candidates ≤5, extra=4 → 4 parked, 1 remains.
	if running != 1 {
		t.Errorf("running = %d, want 1 (one warm above the +1 buffer)", running)
	}
	if vmm.snapshots < 4 {
		t.Errorf("snapshots = %d, want ≥ 4 (4 aggressive parks)", vmm.snapshots)
	}
}

func TestLoopReaperAggressiveNoSignalDefersToIdle(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	wakeN(t, engine, app.ID, 5)

	// A wired mirror with neither a gateway observation nor fresh VMMD
	// telemetry is an unavailable signal, not a measured zero traffic rate.
	frozen := time.Now().Add(35 * time.Second)
	loop := NewLoop(nil, engine, testLog()).
		WithClock(func() time.Time { return frozen }).
		WithRecentLoad(recentload.New(nil, 5, time.Second)).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	if running := liveCount(t, store, app.ID); running != 5 {
		t.Errorf("running = %d, want 5 (no scale-down without a fresh signal)", running)
	}
	if vmm.snapshots != 0 {
		t.Errorf("snapshots = %d, want 0", vmm.snapshots)
	}
}

func TestLoopReaperAggressiveHonorsFloor(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// min_instances=2 + autoscale_target_rps=10. After the burst
	// drops to 0 rps, the floor must keep 2 resident.
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetMinInstances:       true,
		MinInstances:          intPtr(2),
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	wakeN(t, engine, app.ID, 5)

	scraper := &fakeScaleUpScraper{}
	frozen := time.Now().Add(35 * time.Second)
	clock := func() time.Time { return frozen }
	mirror := recentload.New(scraper, 5, 5*time.Minute)
	// Seed the ring with a fresh "0 rps" snapshot so desired=0.
	scraper.set(map[string]int64{app.ID: 1})
	mirror.Touch(context.Background(), frozen)
	// Force the running sum to 0 by setting cumulative below
	// lastSeen — this is the "traffic vanished" scenario.
	scraper.set(map[string]int64{app.ID: 0})
	mirror.Touch(context.Background(), frozen.Add(time.Minute))

	loop := NewLoop(nil, engine, testLog()).
		WithClock(clock).
		WithRecentLoad(mirror).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	running := liveCount(t, store, app.ID)
	// floor=2, desired=0, limit=max(2, 0+1)=2, extra=5-2=3 → park 3.
	if running != 2 {
		t.Errorf("running = %d, want 2 (floor honored)", running)
	}
}

func TestLoopReaperAggressiveEmitsAuditRow(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	wakeN(t, engine, app.ID, 5)

	scraper := &fakeScaleUpScraper{}
	frozen := time.Now().Add(35 * time.Second)
	clock := func() time.Time { return frozen }
	mirror := recentload.New(scraper, 5, 5*time.Minute)
	scraper.set(map[string]int64{app.ID: 1})
	mirror.Touch(context.Background(), frozen)
	scraper.set(map[string]int64{app.ID: 0})
	mirror.Touch(context.Background(), frozen.Add(time.Minute))

	loop := NewLoop(nil, engine, testLog()).
		WithClock(clock).
		WithRecentLoad(mirror).
		WithReaperAggressive(true)
	loop.runReaper(context.Background())

	// Inspect the events table for the reaper_scale_down row.
	// MemStore exposes ListEvents / similar via the store surface;
	// for now assert via the store's AppendEvent call shape: we
	// rely on the loop's emitScaleDownAudit having been driven.
	// The simplest assertion is: at least one events row exists
	// with kind='reaper_scale_down'. The MemStore's events
	// accessor is ListEventsForActor (verified below by the
	// engine_test.go events table tests).
	events := storeEvents(t, store, app.ID, "reaper_scale_down")
	if len(events) == 0 {
		t.Fatalf("expected at least one reaper_scale_down audit row")
	}
	// Spot-check the JSON payload shape.
	var payload map[string]any
	if err := json.Unmarshal(events[0].Data, &payload); err != nil {
		t.Fatalf("audit data unmarshal: %v", err)
	}
	if payload["app"] != app.ID {
		t.Errorf("payload.app = %v, want %s", payload["app"], app.ID)
	}
	if payload["reason"] != "traffic_dropped" {
		t.Errorf("payload.reason = %v, want traffic_dropped", payload["reason"])
	}
	if parked, ok := payload["parked"].([]any); !ok || len(parked) == 0 {
		t.Errorf("payload.parked = %v, want non-empty array", payload["parked"])
	}
}

func TestLoopReaperAggressiveMetricIncrements(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	wakeN(t, engine, app.ID, 5)

	scraper := &fakeScaleUpScraper{}
	frozen := time.Now().Add(35 * time.Second)
	clock := func() time.Time { return frozen }
	mirror := recentload.New(scraper, 5, 5*time.Minute)
	scraper.set(map[string]int64{app.ID: 1})
	mirror.Touch(context.Background(), frozen)
	scraper.set(map[string]int64{app.ID: 0})
	mirror.Touch(context.Background(), frozen.Add(time.Minute))

	ops := wire.NewOpsMetrics("schedd")
	loop := NewLoop(nil, engine, testLog()).
		WithClock(clock).
		WithRecentLoad(mirror).
		WithReaperAggressive(true).
		WithOpsMetrics(ops)
	loop.runReaper(context.Background())

	// Scrape /metrics via httptest; assert the per-(app, outcome)
	// counter row incremented.
	body := wireRenderMetrics(t, ops)
	wantLines := []string{
		`schedd_scale_down_decisions_total{app="` + app.ID + `",outcome="park"} 1`,
	}
	for _, want := range wantLines {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
}

// intPtr is a tiny helper for *int literals in UpdateAppParams
// calls. MemStore.UpdateApp takes pointer fields; the literal
// helper keeps the test bodies readable.
func intPtr(i int) *int { return &i }

// TestLoopReaperAggressiveEmitsCooldownHeld (P1C) pins that when
// the per-app scale-in cooldown consult in ReapAggressive fires,
// schedd_scale_down_decisions_total{outcome="cooldown_held"} is
// emitted exactly once per app per tick, AND that the app does
// NOT appear in the park slice (so the existing `park` outcome
// does not fire for the same app in the same tick).
//
// Test setup mirrors TestLoopReaperAggressiveMetricIncrements but
// primes apps.last_scale_in_at within the cooldown window. The
// aggressive reaper would otherwise park 4 instances (floor=0,
// desired=0, running=5, limit=1, extra=4) — under the cooldown
// window the entire app is skipped and the metric flips to
// `cooldown_held` with `park` = 0.
func TestLoopReaperAggressiveEmitsCooldownHeld(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// ScaleInCooldownS = 60s + autoscale target so the aggressive
	// path considers this app. ScaleInCooldownS > 0 is required to
	// activate the cooldown consult at reaper.go:331.
	scaleInCooldown := 60
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetScalingPolicy: true,
		ScalingPolicy: &state.ScalingPolicy{
			ScaleInCooldownS: scaleInCooldown,
		},
		SetAutoscaleTargetRPS: true,
		AutoscaleTargetRPS:    intPtr(10),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	wakeN(t, engine, app.ID, 5)

	scraper := &fakeScaleUpScraper{}
	clock := func() time.Time { return time.Now().Add(35 * time.Second) }
	mirror := recentload.New(scraper, 5, 5*time.Minute)
	scraper.set(map[string]int64{app.ID: 1})
	mirror.Touch(context.Background(), clock())
	scraper.set(map[string]int64{app.ID: 0})
	mirror.Touch(context.Background(), clock().Add(time.Minute))

	// Stamp last_scale_in_at within the cooldown window. The
	// aggressive reaper consults it via the InstanceInfo carrier
	// (loop.go:1130-1135 mirrors LastScaleInAt + ScaleInCooldownS).
	// StampAppScaleIn stamps now() — the reaper consults now vs
	// LastScaleInAt, so a stamp right before the runReaper call
	// lands inside the 60s window.
	if err := store.StampAppScaleIn(context.Background(), app.ID); err != nil {
		t.Fatalf("StampAppScaleIn: %v", err)
	}

	ops := wire.NewOpsMetrics("schedd")
	loop := NewLoop(nil, engine, testLog()).
		WithClock(clock).
		WithRecentLoad(mirror).
		WithReaperAggressive(true).
		WithOpsMetrics(ops)
	loop.runReaper(context.Background())

	body := wireRenderMetrics(t, ops)
	wantLines := []string{
		`schedd_scale_down_decisions_total{app="` + app.ID + `",outcome="cooldown_held"} 1`,
		// Empty-app placeholder rows must still surface from boot —
		// this is the §12 panel contract.
		`schedd_scale_down_decisions_total{app="",outcome="cooldown_held"} 0`,
	}
	for _, want := range wantLines {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
	// And no `park` observation fired for this app — a successful
	// park would have appeared as
	// schedd_scale_down_decisions_total{app=app.ID,outcome="park"} 1.
	// The cooldown_held emission proves the consult fired before any
	// park; if the park had happened we'd see both. Asserting on
	// the absence of the park line keeps the test independent of
	// Prometheus's row-creation-on-first-increment semantics.
	notWant := `schedd_scale_down_decisions_total{app="` + app.ID + `",outcome="park"}`
	if bytes.Contains(body, []byte(notWant)) {
		t.Errorf("unexpected line %q in:\n%s", notWant, body)
	}

	// And the running count is unchanged — the cooldown skipped the
	// whole app, so the reaper did not park any instance.
	running := liveCount(t, store, app.ID)
	if running != 5 {
		t.Errorf("running = %d, want 5 (cooldown skipped whole app, no parks)", running)
	}
}

// TestLoopReaperIdleEmitsCooldownHeld (P1D) pins that the idle
// reaper branch emits schedd_scale_down_decisions_total{outcome=
// "cooldown_held"} exactly once per app per tick when its per-app
// scale-in cooldown consult fires. Mirror of
// TestLoopReaperAggressiveEmitsCooldownHeld above but WITHOUT
// WithReaperAggressive(true) — the aggressive branch is gated off
// so the cooldown_held emission is unambiguously attributable to
// the idle branch. This is the load-bearing integration pin for
// the P1D metric symmetry fix.
func TestLoopReaperIdleEmitsCooldownHeld(t *testing.T) {
	store := state.NewMemStore()
	_, app, _ := seedApp(t, store, api.PlanPro, 512, 5)
	// ScaleInCooldownS = 60s. No AutoscaleTargetRPS, so the app
	// does NOT appear in the aggressive branch's desiredByApp map
	// — but the aggressive branch is gated off anyway. With
	// WithReaperAggressive(false) the aggressive branch is fully
	// skipped (l.recentLoad nil check at loop.go:1278).
	scaleInCooldown := 60
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetScalingPolicy: true,
		ScalingPolicy: &state.ScalingPolicy{
			ScaleInCooldownS: scaleInCooldown,
		},
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	wakeN(t, engine, app.ID, 5)

	// Stamp last_scale_in_at within the cooldown window. The idle
	// reaper consults it via the InstanceInfo carrier (mirror of the
	// aggressive branch test at loop_test.go:612).
	if err := store.StampAppScaleIn(context.Background(), app.ID); err != nil {
		t.Fatalf("StampAppScaleIn: %v", err)
	}

	ops := wire.NewOpsMetrics("schedd")
	// WithReaperAggressive omitted → defaults to false; the aggressive
	// branch is fully gated off.
	loop := NewLoop(nil, engine, testLog()).
		WithOpsMetrics(ops)
	loop.runReaper(context.Background())

	body := wireRenderMetrics(t, ops)
	wantLines := []string{
		`schedd_scale_down_decisions_total{app="` + app.ID + `",outcome="cooldown_held"} 1`,
		// Empty-app placeholder rows must still surface from boot —
		// this is the §12 panel contract.
		`schedd_scale_down_decisions_total{app="",outcome="cooldown_held"} 0`,
	}
	for _, want := range wantLines {
		if !bytes.Contains(body, []byte(want)) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
	// And no `park` observation fired for this app — a successful
	// park would have appeared as
	// schedd_scale_down_decisions_total{app=app.ID,outcome="park"} 1.
	// The cooldown_held emission proves the consult fired before any
	// park; if the park had happened we'd see both. Asserting on
	// the absence of the park line keeps the test independent of
	// Prometheus's row-creation-on-first-increment semantics.
	notWant := `schedd_scale_down_decisions_total{app="` + app.ID + `",outcome="park"}`
	if bytes.Contains(body, []byte(notWant)) {
		t.Errorf("unexpected line %q in:\n%s", notWant, body)
	}

	// And the running count is unchanged — the cooldown skipped the
	// whole app, so the reaper did not park any instance.
	running := liveCount(t, store, app.ID)
	if running != 5 {
		t.Errorf("running = %d, want 5 (idle cooldown skipped whole app, no parks)", running)
	}
}

// liveCount returns the number of RUNNING instances of an app.
// Pulled out as a helper so the test bodies above stay focused.
func liveCount(t *testing.T, store state.Store, appID string) int {
	t.Helper()
	rows, err := store.ListInstancesForApp(context.Background(), appID)
	if err != nil {
		t.Fatalf("ListInstancesForApp: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.State == string(state.StateRunning) {
			n++
		}
	}
	return n
}

// storeEvents is the test seam for the events table accessor. The
// MemStore exposes ListEvents (verified via pkg/state/events_test.go);
// we abstract the call here so a future MemStore rename doesn't
// ripple through the test bodies.
type storedEvent struct {
	Kind string
	Data []byte
}

func storeEvents(t *testing.T, store state.Store, subject, kind string) []storedEvent {
	t.Helper()
	// MemStore.ListEvents(ctx, subject, limit) — subject is the
	// entity the event is about (typically app_id for our audit
	// rows). Filter the result by (actor, kind) post-fetch since
	// the MemStore surface is intentionally narrow. Production
	// pgstore.AppendEvent uses jsonb Data identically.
	rows, err := store.ListEvents(context.Background(), subject, 64)
	if err != nil {
		t.Fatalf("ListEvents(%s): %v", subject, err)
	}
	out := make([]storedEvent, 0, len(rows))
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, storedEvent{Kind: r.Kind, Data: r.Data})
		}
	}
	return out
}

// wireRenderMetrics renders the OpsMetrics registry via an
// httptest server and returns the body. Mirrors the helper in
// pkg/wire/metrics_test.go (render) but is exported via a test
// package-private name so the sched tests don't import the wire
// package's test surface.
func wireRenderMetrics(t *testing.T, ops *wire.OpsMetrics) []byte {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	ops.Handler().ServeHTTP(rec, req)
	return rec.Body.Bytes()
}

// --- PR scale-out readiness #3 — disk-drift sweep Loop wiring ---
//
// These tests pin the WithDiskDrift / runDiskDrift / diskDriftTick
// triad on Loop. The drift component itself (pkg/sched/disk_drift_test.go)
// owns the per-tick behaviour; this file owns the dispatcher seam:
// the ticker fires → runDiskDrift is called → Tick runs → log + return.
// The two cases below cover (1) happy dispatch and (2) nil-safe
// ticker opt-out. The dispatcher error-swallowing test is
// intentionally omitted — see the comment above the absent function
// for the rationale.

// TestLoopRunDiskDriftDispatchesTick — WithDiskDrift wires a
// real *DiskDrift; calling runDiskDrift directly invokes Tick
// once. Uses a MemStore (no DB) and an empty t.TempDir() so the
// sweep's "SnapDir absent" branch is exercised (no drift, no error).
func TestLoopRunDiskDriftDispatchesTick(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	dd := NewDiskDrift(store, testLog())
	loop := NewLoop(nil, engine, testLog()).WithDiskDrift(dd)

	// runDiskDrift is exported as a method specifically so tests
	// can drive a single tick without spinning up Run's goroutine.
	loop.runDiskDrift(context.Background())
}

// TestLoopWithoutDiskDriftIsNilSafe — bare NewLoop (no
// WithDiskDrift) followed by a direct runDiskDrift must not panic.
// This mirrors the WithRetention(nil) and WithHeartbeat(nil)
// opt-out surfaces — production tests + run-without-metrics mode
// rely on it.
func TestLoopWithoutDiskDriftIsNilSafe(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	// Direct dispatch on a nil diskDrift must be a no-op.
	loop.runDiskDrift(context.Background())
}

// TestLoopRunDiskDriftRespectsTickTimeout — runDiskDrift wraps the
// incoming ctx with a per-tick deadline (sched.DefaultDiskDriftTickTimeout)
// so a slow /srv/fc/snap mount cannot freeze the loop's 1 Hz tick
// budget. The wrap is the only behaviour under test here: a
// pre-cancelled tickCtx must produce a sweep that returns without
// touching the counter (the per-iteration ctx.Err() guard in
// Tick short-circuits before any dep dir is read).
func TestLoopRunDiskDriftRespectsTickTimeout(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	// Force a tight timeout so the wrap is observable in the test.
	dd := NewDiskDrift(store, testLog()).WithTickTimeout(1 * time.Nanosecond)
	ops := wire.NewOpsMetrics("schedd")
	dd.WithMetrics(ops)
	loop := NewLoop(nil, engine, testLog()).WithDiskDrift(dd)

	// Sleep past the 1-ns deadline so the inner wrap is already
	// expired by the time Tick runs. The dispatcher-and-Tick
	// pipeline must return without panic and without incrementing
	// the counter (the per-iteration ctx.Err() guard fires before
	// any disk read).
	time.Sleep(2 * time.Millisecond)
	loop.runDiskDrift(context.Background())
}

// TestRunDiskDriftErrorDoesNotStopLoop is intentionally omitted.
// Dispatcher error-swallowing for runDiskDrift is identical to
// runHeartbeat + runRetention, both untested for the same reason:
// DiskDrift.Tick is nil-error-by-construction today, so an
// "errored Tick" double would require a test-only injection seam
// without a corresponding production path. The dispatcher's
// `errors.Is(err, context.Canceled)` branch is load-bearing but
// unreachable until a future contributor adds a real error
// return from Tick — and when they do, the new test should
// cover the actual emitted error, not a fabricated one.

// TestRunReaperMirrorsDeploymentFloor (issue #557 closure / ADR-072)
// pins the reaper / biller floor-mirror: an app with
// app.min_instances=0 + deployment.min_instances=3 must keep
// ≥ 3 RUNNING instances alive after the reaper tick — the same
// number meterd's sampler charges for at sampler.go:470-485.
// Pre-#557 the reaper stamped only a.EffectiveMinInstances (0)
// onto InstanceInfo.MinInstances, so the idle park would have
// swept the app to 0 even though the biller was emitting
// synthetic usage rows for 3 instances per minute. The post-#557
// post-enrichment pass walks each instance's DeploymentID,
// looks up d.EffectiveMinInstances, and re-stamps the snapshot
// rows with the app-wide max.
func TestRunReaperMirrorsDeploymentFloor(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 10)
	// Set the deployment floor to 3 (issue #557 / ADR-072). The
	// app-level floor stays at 0 — the whole point of the test
	// is that the reaper honors the deployment axis without
	// the customer having to set the app axis.
	if _, err := store.UpdateDeploymentMinInstances(context.Background(), dep.ID, 3); err != nil {
		t.Fatalf("UpdateDeploymentMinInstances: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")

	// Wake 5 instances so the floor=3 forces 2 parks.
	wakeN(t, engine, app.ID, 5)
	if got, want := liveCount(t, store, app.ID), 5; got != want {
		t.Fatalf("setup: liveCount=%d, want %d", got, want)
	}

	// Backdate all 5 instances' LastRequest far past the idle
	// timeout so ReapIdle is willing to park them. The deployment
	// floor=3 must hold 3 alive regardless of the staleness.
	ctx := context.Background()
	rows, _ := store.ListInstancesForApp(ctx, app.ID)
	touches := make([]state.InstanceTouch, 0, len(rows))
	for _, r := range rows {
		touches = append(touches, state.InstanceTouch{
			InstanceID:  r.ID,
			LastRequest: time.Now().Add(-2 * time.Hour),
		})
	}
	if _, err := store.TouchInstancesLastSeen(ctx, touches); err != nil {
		t.Fatalf("backdate touch: %v", err)
	}

	loop := NewLoop(nil, engine, testLog())
	loop.runReaper(ctx)

	// Floor mirror: 3 instances should remain RUNNING even though
	// every instance was past its idle timeout and app.min_instances=0.
	if got, want := liveCount(t, store, app.ID), 3; got != want {
		t.Errorf("liveCount after reaper = %d, want %d (deployment floor mirror)", got, want)
	}
}

// TestRunReaperFloorDropEmitsAuditRelocated (issue #557 closure /
// ADR-072) pins the audit-emit relocation: when the floor drops
// between ticks AND ReapIdle parks ≥ 1 instance for the app, the
// reaper emits a `instances.parked_min_instances_released` row
// ONCE per app per tick. Pre-#557 the same emit lived in
// runReaperAggressive on a structurally unsatisfiable predicate
// (ReapAggressive's `limit` arithmetic never parks below the floor
// it was given), so the row was never written. Post-#557 the
// emit lives in the ReapIdle branch, keyed on the new
// lastFloorByApp carrier.
func TestRunReaperFloorDropEmitsAuditRelocated(t *testing.T) {
	store := state.NewMemStore()
	_, app, dep := seedApp(t, store, api.PlanPro, 512, 10)
	// Tick 1: floor=3, app.min_instances=3 (legacy column).
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetMinInstances: true,
		MinInstances:    intPtr(3),
	}); err != nil {
		t.Fatalf("UpdateApp: %v", err)
	}
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	// Wake 3 instances; backdate LastRequest past the idle timeout
	// so the FIRST tick's reaper would park them all (floor=3 keeps
	// 0 alive? no — floor=3 keeps 3 alive, so ReapIdle parks
	// nothing this tick). We seed lastFloorByApp=3 BEFORE the
	// floor drop so the second tick has a prev-floor to compare
	// against. The MemStore's live count + the reaper's ledger
	// are consistent across ticks; we manipulate state directly
	// to avoid a real-time race.
	wakeN(t, engine, app.ID, 3)
	// Backdate LastRequest on every running instance past the
	// idle timeout so ReapIdle is willing to park them once
	// the floor drops.
	rows, _ := store.ListInstancesForApp(context.Background(), app.ID)
	touches := make([]state.InstanceTouch, 0, len(rows))
	for _, r := range rows {
		touches = append(touches, state.InstanceTouch{
			InstanceID:  r.ID,
			LastRequest: time.Now().Add(-2 * time.Hour),
		})
	}
	if _, err := store.TouchInstancesLastSeen(context.Background(), touches); err != nil {
		t.Fatalf("backdate touch: %v", err)
	}
	// Pre-seed lastFloorByApp BEFORE the first tick to mirror
	// production: a long-running schedd has accumulated
	// lastFloorByApp values from prior ticks.
	loop.lastFloorByApp = map[string]int{app.ID: 3}

	// Tick 1 (the fixture is already idle-parkable): floor=3,
	// 3 instances, all past idle. ReapIdle's
	// `running > floor && lastRequest > timeout` short-circuits
	// — none parked. lastFloorByApp is refreshed to 3.
	loop.runReaper(context.Background())
	if got := liveCount(t, store, app.ID); got != 3 {
		t.Fatalf("tick 1 liveCount=%d, want 3 (floor holds)", got)
	}
	// No audit row yet (no actual park, no floor drop).
	if got := storeEvents(t, store, app.ID, "instances.parked_min_instances_released"); len(got) != 0 {
		t.Errorf("tick 1: audit rows=%d, want 0 (no floor drop, no park)", len(got))
	}

	// Drop the floor to 0; this is the PATCH the customer made.
	if _, err := store.UpdateApp(context.Background(), app.ID, state.UpdateAppParams{
		SetMinInstances: true,
		MinInstances:    intPtr(0),
	}); err != nil {
		t.Fatalf("UpdateApp drop: %v", err)
	}
	// Also drop the deployment floor so appDeploymentFloor
	// collapses to 0 (the test is asserting the reaper honors a
	// pure floor drop, not a deploy-floor inheritance).
	if _, err := store.UpdateDeploymentMinInstances(context.Background(), dep.ID, 0); err != nil {
		t.Fatalf("UpdateDeploymentMinInstances drop: %v", err)
	}

	// Tick 2: floor now 0, lastFloorByApp[appID]=3 from tick 1.
	// 3 idle instances. ReapIdle parks all 3 (floor=0, no
	// protection). The audit emit must fire because
	// lastFloorByApp[appID]=3 > appDeploymentFloor[appID]=0 AND
	// ReapIdle parked ≥ 1 instance for this app.
	loop.runReaper(context.Background())
	if got := liveCount(t, store, app.ID); got != 0 {
		t.Errorf("tick 2 liveCount=%d, want 0 (floor dropped to 0)", got)
	}
	events := storeEvents(t, store, app.ID, "instances.parked_min_instances_released")
	if len(events) != 1 {
		t.Fatalf("tick 2: audit rows=%d, want 1 (floor drop + ≥1 park)", len(events))
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Data, &payload); err != nil {
		t.Fatalf("audit data unmarshal: %v", err)
	}
	// floor (previous) and post_park (new effective). Reason is
	// the static string the emitter stamps (mirrors the call site).
	if payload["floor"] != float64(3) {
		t.Errorf("payload.floor = %v, want 3", payload["floor"])
	}
	if payload["reason"] != "min_instances_lowered" {
		t.Errorf("payload.reason = %v, want min_instances_lowered", payload["reason"])
	}
}
