package sched

// adr: 090 Manual fire-now bypasses the schedule boundary without moving it.
// Tests for the fire-now side of issue #791 PR-C / ADR-090.
//
// Coverage split:
//   - RunCronNow (pkg/sched/loop.go) — the per-row dispatch helper.
//   - drainPendingFireNowRequests (pkg/sched/fire_now.go) — the
//     claim+dispatch loop the subscriber goroutine calls.
//
// We do NOT test FireNowRun itself: it requires a real Postgres to
// exercise db.SubscribeWithReconnect. The unit-test coverage stops at
// the helpers FireNowRun composes, which is the right place for both
// behavioural regressions and CI speed (no pg dependency).
//
// Why MemStore everywhere: pgstore_fire_now.go's claim is a thin SQL
// wrapper over the SKIP LOCKED + status filter, and the test the
// MemStore gets (FIFO order, status guard, error mapping) carries
// over to PgStore by construction.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// ---------- RunCronNow ----------

func TestRunCronNow_FiresImmediateSkipBoundary(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "fnt@example.com", api.PlanPro)

	// Cron scheduled for a year in the future — the 60s tick would
	// never fire it within the test horizon. RunCronNow must bypass
	// the due-time boundary guard and dispatch anyway.
	app, c := newAppAndCron(t, store, acct.ID, true)
	// newAppAndCron backdates CreatedAt to 11:00 for the tick path.
	// Override the schedule so next_fire_at is far in the future.
	yearAhead := "0 0 1 1 *"
	if _, err := store.UpdateCron(ctx, c.ID, nil, &yearAhead, nil, nil); err != nil {
		t.Fatalf("update schedule: %v", err)
	}

	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &recordingSynth{}
	loop := NewLoop(nil, eng, slog.Default()).
		WithGatewaySynth(synth)

	cronAfterUpdate, err := store.CronByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("CronByID: %v", err)
	}

	// Sanity: next_fire_at is genuinely > test-end time. Schedule
	// "0 0 1 1 *" = midnight on Jan 1 — robfig picks the *next*
	// occurrence which is at least a year out.
	if !cronAfterUpdate.LastFiredAt.IsZero() {
		t.Fatalf("LastFiredAt pre-fire = %v, want zero (precondition: cron has never fired)", cronAfterUpdate.LastFiredAt)
	}

	out, err := loop.RunCronNow(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("RunCronNow: %v", err)
	}
	if !out.Success {
		t.Errorf("Success = false, want true; out=%+v", out)
	}

	// Synth must have been invoked once — the dispatch path reached
	// the synth step.
	if got := synth.calls.Load(); got != 1 {
		t.Errorf("synth calls = %d, want 1 (manual fire should reach the gateway-Invoke step)", got)
	}

	// Hard rule (ADR-090 §"Reuse of schedd"): RunCronNow does NOT call
	// MarkCronFired. A manual fire must not shift next_fire_at or
	// stick a wall-clock stamp on LastFiredAt. Confirm by reloading
	// and re-running the tick path — if LastFiredAt got stamped, the
	// tick would skip the cron for at least one boundary.
	cronAfter, err := store.CronByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("CronByID post-fire: %v", err)
	}
	if !cronAfter.LastFiredAt.IsZero() {
		t.Errorf("LastFiredAt post-fire = %v, want zero — RunCronNow must not call MarkCronFired (ADR-090 §'Reuse of schedd')",
			cronAfter.LastFiredAt)
	}

	// And the cron row should still belong to the same app (sanity).
	if cronAfter.AppID != app.ID {
		t.Errorf("AppID = %q, want %q", cronAfter.AppID, app.ID)
	}
}

func TestRunCronNow_DisabledCronReturnsErrCronDisabled(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "fnd@example.com", api.PlanPro)
	_, c := newAppAndCron(t, store, acct.ID, false) // enabled=false

	eng, _ := makeEngine(t, store, &fakeWakeVMM{})
	loop := NewLoop(nil, eng, slog.Default())

	_, err := loop.RunCronNow(ctx, c.ID, acct.ID)
	if !errors.Is(err, ErrCronDisabled) {
		t.Fatalf("err = %v, want ErrCronDisabled", err)
	}
}

func TestRunCronNow_UnknownCronReturnsStoreError(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "fnu@example.com", api.PlanPro)
	eng, _ := makeEngine(t, store, &fakeWakeVMM{})
	loop := NewLoop(nil, eng, slog.Default())

	// "no-such-cron" is a valid UUID-shape-but-absent id. CronByID
	// must return ErrNotFound and RunCronNow bubbles that up.
	_, err := loop.RunCronNow(ctx, "no-such-cron", acct.ID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("err = %v, want state.ErrNotFound", err)
	}
}

// ---------- drainPendingFireNowRequests ----------

// fakeHarness bundles the engine + loop + store + fakes used by the
// drainPendingFireNowRequests tests. Kept tiny on purpose — each
// test field-overrides only the bits it cares about.
type fireNowHarness struct {
	store state.Store
	loop  *Loop
	synth *recordingSynth
	vmm   *fakeWakeVMM
	now   time.Time
}

func newFireNowHarness(t *testing.T) *fireNowHarness {
	t.Helper()
	store := state.NewMemStore()
	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &recordingSynth{}
	loop := NewLoop(nil, eng, slog.Default()).WithGatewaySynth(synth)
	return &fireNowHarness{
		store: store, loop: loop, synth: synth, vmm: vmm,
		now: time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC),
	}
}

// drainPendingFireNowRequests needs a CronByID that returns the cron
// we seeded. MemStore's CronByID matches on the row stored by
// CreateCron, so the (cronID, accountID) pair is the only thing
// drain needs — it reads the request row, calls RunCronNow, and
// stamps the terminal state. We don't need to wire a fake CronByID.

func TestDrainPending_NoRowsIsClean(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	// Drain on an empty store: returns immediately. The contract is
	// "exits cleanly with no error and no Mark* calls". Asserted by
	// the absence of panic + zero synth calls.
	h.loop.drainPendingFireNowRequests(context.Background())
	if got := h.synth.calls.Load(); got != 0 {
		t.Errorf("synth calls = %d, want 0 on empty queue", got)
	}
}

func TestDrainPending_HappyPathStampsSucceeded(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	ctx := context.Background()
	acct, _ := h.store.CreateAccount(ctx, "dph@example.com", api.PlanPro)
	_, c := newAppAndCron(t, h.store, acct.ID, true)

	// apid's fireCronNow would have created this row. We simulate
	// the apid side here so the test focuses on schedd's behaviour.
	requestID, err := h.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	h.loop.drainPendingFireNowRequests(ctx)

	// Row is now terminal: succeeded (RunCronNow returned Success).
	got, err := h.store.GetFireNowRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if got.Status != state.FireNowStatusSucceeded {
		t.Errorf("status = %q, want succeeded", got.Status)
	}
	if got.FinishedAt == nil {
		t.Errorf("FinishedAt nil, want terminal stamp")
	}

	// Synth was invoked exactly once: drain's claim+dispatch path
	// reached the gateway Invoke step.
	if n := h.synth.calls.Load(); n != 1 {
		t.Errorf("synth calls = %d, want 1", n)
	}

	// Hard rule (re-stated from the unit test above): a manual fire
	// must NOT shift LastFiredAt — drain's dispatch goes through
	// RunCronNow which never calls MarkCronFired.
	post, err := h.store.CronByID(ctx, c.ID)
	if err != nil {
		t.Fatalf("CronByID post-drain: %v", err)
	}
	if !post.LastFiredAt.IsZero() {
		t.Errorf("LastFiredAt post-drain = %v, want zero (RunCronNow must not MarkCronFired)", post.LastFiredAt)
	}
}

func TestDrainPending_CommandCronQueuesTaskAndPreservesScheduleCursor(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	ctx := context.Background()
	_, app, deployment := seedApp(t, h.store, api.PlanPro, 256, 1)
	if err := h.store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/fire-now", "apps/fire-now.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := h.store.CreateCronWithOptions(ctx, app.ID, "0 0 1 1 *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance", "--compact"}, RetryMax: 2, RetryBackoffSeconds: 30,
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	lastFiredAt := time.Date(2026, 9, 24, 8, 0, 0, 0, time.UTC)
	if err := h.store.MarkCronFired(ctx, cron.ID, lastFiredAt); err != nil {
		t.Fatalf("set schedule cursor: %v", err)
	}
	requestID, err := h.store.InsertFireNowRequest(ctx, cron.ID, app.AccountID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	h.loop.drainPendingFireNowRequests(ctx)

	request, err := h.store.GetFireNowRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if request.Status != state.FireNowStatusSucceeded || request.TaskID == nil || *request.TaskID == "" || request.InvocationID != nil {
		t.Fatalf("fire-now receipt = %+v; want success with a task id and no invocation id", request)
	}
	tasks, err := h.store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("command cron tasks = %d, %v; want one", len(tasks), err)
	}
	task := tasks[0]
	if task.ID != *request.TaskID || task.DeploymentID != deployment.ID || task.Status != state.AppTaskQueued ||
		task.ScheduledFor != nil || task.RetryMax != 2 || task.RetryBackoffSeconds != 30 ||
		len(task.Command) != 2 || task.Command[1] != "--compact" {
		t.Fatalf("manual command task = %+v; want the saved command, retries, live deployment, and no scheduled_for", task)
	}
	cronAfter, err := h.store.CronByID(ctx, cron.ID)
	if err != nil || !cronAfter.LastFiredAt.Equal(lastFiredAt) {
		t.Fatalf("LastFiredAt after manual fire = %v, %v; want unchanged %v", cronAfter.LastFiredAt, err, lastFiredAt)
	}
	if h.synth.calls.Load() != 0 {
		t.Errorf("HTTP synth calls = %d; command cron fire-now must enqueue an app task", h.synth.calls.Load())
	}
}

func TestDrainPending_CommandCronRespectsSkipIfRunning(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	ctx := context.Background()
	_, app, deployment := seedApp(t, h.store, api.PlanPro, 256, 1)
	if err := h.store.SetDeploymentRootfs(ctx, deployment.ID, "/rootfs/fire-now-overlap", "apps/fire-now-overlap.ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs: %v", err)
	}
	cron, err := h.store.CreateCronWithOptions(ctx, app.ID, "0 0 1 1 *", "", true, state.CronOptions{
		Command: []string{"bin/maintenance"}, SkipIfRunning: true,
	})
	if err != nil {
		t.Fatalf("CreateCronWithOptions: %v", err)
	}
	if _, created, err := h.store.CreateScheduledCronAppTask(ctx, cron.ID, nil, time.Now().UTC()); err != nil || !created {
		t.Fatalf("seed active scheduled task: created=%t err=%v", created, err)
	}
	requestID, err := h.store.InsertFireNowRequest(ctx, cron.ID, app.AccountID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	h.loop.drainPendingFireNowRequests(ctx)

	request, err := h.store.GetFireNowRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if request.Status != state.FireNowStatusFailed || request.Error == nil || !strings.Contains(*request.Error, "active run") {
		t.Fatalf("overlap fire-now receipt = %+v; want failed overlap", request)
	}
	tasks, err := h.store.ListCronAppTaskRuns(ctx, cron.ID, 10, "")
	if err != nil || len(tasks) != 1 {
		t.Fatalf("command cron tasks after overlap = %d, %v; want only the original task", len(tasks), err)
	}
}

func TestDrainPending_DisabledCronStampsCronDisabled(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	ctx := context.Background()
	acct, _ := h.store.CreateAccount(ctx, "dpd@example.com", api.PlanPro)
	_, c := newAppAndCron(t, h.store, acct.ID, false) // disabled
	requestID, err := h.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	h.loop.drainPendingFireNowRequests(ctx)

	got, err := h.store.GetFireNowRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if got.Status != state.FireNowStatusFailed {
		t.Fatalf("status = %q, want failed (ErrCronDisabled is mapped to failed)", got.Status)
	}
	if got.Error == nil || *got.Error != "cron disabled" {
		t.Errorf("Error = %v, want pointer to \"cron disabled\"", got.Error)
	}
	// Synth must NOT have fired — RunCronNow returned before reaching
	// dispatchCronLocked.
	if n := h.synth.calls.Load(); n != 0 {
		t.Errorf("synth calls = %d, want 0 (disabled cron must not invoke)", n)
	}
}

func TestDrainPending_AllRowsDrained(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	ctx := context.Background()
	acct, _ := h.store.CreateAccount(ctx, "dpall@example.com", api.PlanPro)
	_, c := newAppAndCron(t, h.store, acct.ID, true)

	// Seed three requests — drain must claim all of them in FIFO
	// order in a single call (ClaimPendingFireNowRequest is one-row
	// per call but the drain loop calls it repeatedly until empty).
	ids := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		id, err := h.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
		if err != nil {
			t.Fatalf("InsertFireNowRequest #%d: %v", i, err)
		}
		ids = append(ids, id)
		// Strict ordering — MemStore's FIFO-by-requested_at relies on
		// RequestedAt being monotonically non-decreasing across
		// consecutive inserts. MemStore uses time.Now() so a 1ms
		// gap is enough on any reasonable clock.
		time.Sleep(1 * time.Millisecond)
	}

	h.loop.drainPendingFireNowRequests(ctx)

	for i, id := range ids {
		got, err := h.store.GetFireNowRequest(ctx, id)
		if err != nil {
			t.Fatalf("GetFireNowRequest[%d]: %v", i, err)
		}
		if got.Status != state.FireNowStatusSucceeded {
			t.Errorf("id[%d] status = %q, want succeeded", i, got.Status)
		}
	}
	if n := h.synth.calls.Load(); n != int64(len(ids)) {
		t.Errorf("synth calls = %d, want %d (one per drain)", n, len(ids))
	}
}

// ---------- cronFireNowDispatchDur metric (issue #791 PR-D) ----------

// findHistogram returns the *dto.Histogram metric matching the given
// name + label set, or nil. The metric family slice mirrors the
// /metrics output: gather the registry, scan by metric name, and
// match all label name+value pairs against `want`. This is heavier
// than testutil.ToFloat64 because the dispatch-latency observation is
// a histogram — we need the bucket counts to confirm the histogram
// observed the right bin.
func findHistogram(t *testing.T, m *wire.OpsMetrics, name string, want map[string]string) *dto.Histogram {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Registry.Gather: %v", err)
	}
	for _, fam := range families {
		if !strings.Contains(fam.GetName(), name) {
			continue
		}
		for _, mt := range fam.GetMetric() {
			labels := map[string]string{}
			for _, l := range mt.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			ok := true
			for k, v := range want {
				if labels[k] != v {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			return mt.GetHistogram()
		}
	}
	return nil
}

func histogramSampleCount(h *dto.Histogram) uint64 {
	if h == nil {
		return 0
	}
	return h.GetSampleCount()
}

// TestDrainPending_EmitsDispatchLatencySucceeded pins that a successful
// drain emits exactly one observation labelled result="succeeded" and
// the sample count increases by one per row processed. Coverage for
// ADR-090 §"Sub-decision 7".
func TestDrainPending_EmitsDispatchLatencySucceeded(t *testing.T) {
	t.Parallel()
	ops := wire.NewOpsMetrics("schedd_test_succ")
	h := newFireNowHarness(t)
	h.loop.WithOpsMetrics(ops)
	ctx := context.Background()
	acct, _ := h.store.CreateAccount(ctx, "dbs@example.com", api.PlanPro)
	_, c := newAppAndCron(t, h.store, acct.ID, true)
	requestID, err := h.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	want := map[string]string{"result": "succeeded"}

	before := histogramSampleCount(findHistogram(t, ops, "cron_fire_now_dispatch_duration_seconds", want))

	h.loop.drainPendingFireNowRequests(ctx)

	after := histogramSampleCount(findHistogram(t, ops, "cron_fire_now_dispatch_duration_seconds", want))

	if delta := after - before; delta != 1 {
		t.Errorf("sample count delta = %d, want 1 (requestID=%s)", delta, requestID)
	}
}

// TestDrainPending_EmitsDispatchLatencyFailed pins that a fire-now
// against a disabled cron emits one observation labelled
// result="failed".
func TestDrainPending_EmitsDispatchLatencyFailed(t *testing.T) {
	t.Parallel()
	ops := wire.NewOpsMetrics("schedd_test_fail")
	h := newFireNowHarness(t)
	h.loop.WithOpsMetrics(ops)
	ctx := context.Background()
	acct, _ := h.store.CreateAccount(ctx, "dbf@example.com", api.PlanPro)
	_, c := newAppAndCron(t, h.store, acct.ID, false) // disabled
	_, err := h.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	want := map[string]string{"result": "failed"}

	before := histogramSampleCount(findHistogram(t, ops, "cron_fire_now_dispatch_duration_seconds", want))

	h.loop.drainPendingFireNowRequests(ctx)

	after := histogramSampleCount(findHistogram(t, ops, "cron_fire_now_dispatch_duration_seconds", want))

	if delta := after - before; delta != 1 {
		t.Errorf("failed-labeled sample count delta = %d, want 1", delta)
	}
}

// TestDrainPending_NilOpsDoesNotPanic pins the nil-safety contract
// from CronFireNowDispatchDuration: a schedd test or a wiring race
// in production must not panic when ops is unset.
func TestDrainPending_NilOpsDoesNotPanic(t *testing.T) {
	t.Parallel()
	h := newFireNowHarness(t)
	// No WithOpsMetrics call — loop.ops is nil.
	ctx := context.Background()
	acct, _ := h.store.CreateAccount(ctx, "dbn@example.com", api.PlanPro)
	_, c := newAppAndCron(t, h.store, acct.ID, true)
	requestID, err := h.store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}
	// If the nil-safety contract breaks, this panics.
	h.loop.drainPendingFireNowRequests(ctx)
	got, err := h.store.GetFireNowRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	if got.Status != state.FireNowStatusSucceeded {
		t.Errorf("status = %q, want succeeded (nil-ops must not block drain)", got.Status)
	}
}

// TestDrainPending_DispatchAdmittedButInvokeFailed pins the
// code-review fix: when dispatchCronLocked admitted the fire but the
// invocation did not complete (audit row status="err"), the fire-now
// row must also stamp failed. Previously the row stamped succeeded
// because RunCronNow discarded the fireSucceeded boolean. The test
// exercises the err == nil && run.Success == false branch added by
// PR-D, driven by the package's shared failingSynth (cron_loop_test.go)
// whose Invoke + SynthesizeRequest both error.
func TestDrainPending_DispatchAdmittedButInvokeFailed(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	vmm := &fakeWakeVMM{}
	eng, _ := makeEngine(t, store, vmm)
	synth := &failingSynth{}
	loop := NewLoop(nil, eng, slog.Default()).WithGatewaySynth(synth)
	ctx := context.Background()
	acct, _ := store.CreateAccount(ctx, "ddf@example.com", api.PlanPro)
	_, c := newAppAndCron(t, store, acct.ID, true)
	requestID, err := store.InsertFireNowRequest(ctx, c.ID, acct.ID)
	if err != nil {
		t.Fatalf("InsertFireNowRequest: %v", err)
	}

	loop.drainPendingFireNowRequests(ctx)

	got, err := store.GetFireNowRequest(ctx, requestID)
	if err != nil {
		t.Fatalf("GetFireNowRequest: %v", err)
	}
	// RunCronNow's new semantics propagate fireSucceeded through
	// the CronRun return value. The failingSynth makes the dispatch
	// path return Success=false; the fire-now row must agree.
	if got.Status != state.FireNowStatusFailed {
		t.Errorf("status = %q, want failed (admit-but-invoke-failed must not stamp succeeded)", got.Status)
	}
	if got.Error == nil {
		t.Errorf("Error nil, want pointer to dispatch-failed message")
	}
	if synth.calls.Load() == 0 {
		t.Errorf("failingSynth.calls = 0, want >= 1 (the dispatch path reached the synth step)")
	}
}
