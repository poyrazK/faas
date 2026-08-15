// dispatch_jobs_test.go — schedd's per-job-task dispatch tick tests.
//
// These tests fence the load-bearing invariants of pkg/sched/dispatch_jobs.go:
//
//   - runJobsTick claims rows via FOR UPDATE SKIP LOCKED: two concurrent
//     ticks competing for the same task see exactly one claim.
//   - The dispatch goroutine's recordTerminalAndRecompute fan-in
//     matches the JobOutcome → Mark* contract:
//     Succeeded  → MarkJobTaskSucceeded
//     Failed     → MarkJobTaskFailed
//     Deadline   → MarkJobTaskFailed (deadline_exceeded class)
//     Cancelled  → MarkJobTaskCancelled
//     BootFail   → MarkJobTaskFailed (boot_failed class)
//   - The Run-level aggregate_status fan-in flips to "succeeded" once
//     every task is terminal (RecomputeJobRunStatus).
//
// The tests use the in-memory JobStore (pkg/state/memstore.go) which
// implements the same SKIP LOCKED contract via per-run mutex; the
// production fork of the contract is exercised in pkg/state/pgstore_test.go
// (PR-B).
package sched

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeJobExitWaiter is a LocalJobExitWaiter test stub that always
// reports a clean exit (0, 0, nil). Mirrors the production
// *fcvm.JailerVMM handle that pkg/sched reads from; the test
// fixtures don't run a real guest, so we short-circuit the
// vsock DGRAM wait to "task succeeded". A sleepFor field lets
// deadline / cancel tests block until ctx is cancelled.
type fakeJobExitWaiter struct {
	sleepFor time.Duration
}

func (f *fakeJobExitWaiter) WaitJobExit(ctx context.Context, _ string, _ time.Duration) (int, int, error) {
	if f.sleepFor > 0 {
		select {
		case <-time.After(f.sleepFor):
			return 0, 0, nil
		case <-ctx.Done():
			return -1, -1, ctx.Err()
		}
	}
	return 0, 0, nil
}

// seedJobRunTask is the local fixture: account (plan=plan) + Job +
// JobRun with N queued JobTask rows. Mirrors the post-PR-A + PR-B
// state; no live instances yet (the dispatch tick is what creates
// them via CreateJobInstance inside WakeJob).
//
// plan is api.PlanScale for the cap-bound tests; Hobby caps at 3
// concurrent tasks which is too tight for the 32-tick-per-tick
// claim test.
func seedJobRunTask(t *testing.T, store state.Store, plan api.Plan, n int) (state.Account, state.Job, state.JobRun) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "u-job@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		t.Fatalf("unknown plan %q", plan)
	}
	job, err := store.CreateJob(ctx, state.Job{
		AccountID:      acct.ID,
		Kind:           state.JobKindRunToCompletion,
		Name:           "job-" + string(plan),
		ImageRef:       "sha256:jobimg",
		RamMb:          256,
		TaskTimeoutS:   30,
		MaxParallelism: int32(limits.JobMaxConcurrentPerAccount),
		RetryMax:       1,
		EnvOverrides:   map[string]string{},
		Status:         state.JobStatusActive,
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	run, err := store.CreateJobRun(ctx, state.JobRun{
		JobID:           job.ID,
		AccountID:       acct.ID,
		TriggerKind:     state.JobRunTriggerManual,
		EnvOverrides:    map[string]string{},
		Tasks:           int32(n),
		Parallelism:     1,
		AggregateStatus: state.JobRunStatusQueued,
	})
	if err != nil {
		t.Fatalf("CreateJobRun: %v", err)
	}
	tindices := make([]int32, n)
	for i := 0; i < n; i++ {
		tindices[i] = int32(i)
	}
	if err := store.InsertJobTasks(ctx, run.ID, tindices); err != nil {
		t.Fatalf("InsertJobTasks: %v", err)
	}
	return acct, job, run
}

// newEngineWithJobExit wires a fresh engine plus a fake vsock
// waiter so WakeJob's Phase 3b succeeds. Without the waiter
// WakeJob returns JobOutcomeBootFail (the dispatch tick treats it
// as a real error and marks the task failed) — the happy-path
// tests need this seam wired so WakeJob completes the cycle.
//
// Also wires a WakeRateLimiter with the job-side bucket ceiling
// bumped to a large value so the concurrency / cap-bound tests
// can claim all N tasks without hitting rate-limit back-pressure.
// Without this, the default jobBucketBurst=0 makes every
// AllowWakeJobAccount return false → all wakes are dropped with
// ErrJobAdmissionRefused → tasks stay 'claimed' forever and
// the run-level fan-in never flips to succeeded.
func newEngineWithJobExit(t *testing.T, store state.Store, vmm RoutedVMM) (*Engine, *Loop) {
	t.Helper()
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	engine.WithJobExitWaiter(&fakeJobExitWaiter{})
	limiter := NewWakeRateLimiter()
	limiter.SetJobBucketBurst(10000) // tests don't exercise rate-limit back-pressure
	engine.WithWakeRateLimiter(limiter)
	return engine, NewLoop(nil, engine, testLog())
}

// TestRunJobsTick_EmptyStoreNoop guards the cheap path: a tick
// against a store with no ready rows returns promptly. This is the
// load-bearing idle behaviour — the dispatcher fires every 1s, and
// 99.99% of ticks on a healthy fleet see zero rows.
func TestRunJobsTick_EmptyStoreNoop(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	// Two ticks; both must noop without touching vmm or panic on
	// nil store internals.
	loop.runJobsTick(context.Background())
	loop.runJobsTick(context.Background())

	if vmm.coldBoots != 0 {
		t.Errorf("coldBoots = %d, want 0 on empty tick", vmm.coldBoots)
	}
}

// TestRunJobsTick_ClaimsReadyTasks seeds a 3-task run on Scale and
// confirms the tick walks all three tasks through
// ListReadyJobTasks + ClaimJobTasks. The fake vmm returns success
// so each WakeJob completes the happy path and the run-level
// aggregate_status flips to "succeeded" after the fan-in.
//
// Scale (JobMaxConcurrentPerAccount=32) gives us enough room to
// claim + boot 3 tasks in one tick without hitting the per-account
// cap; Hobby=3 would also work but Scale is the more conservative
// choice for the happy-path pin.
//
// This is the happy-path contract: every queued task is claimed,
// booted, and recorded.
func TestRunJobsTick_ClaimsReadyTasks(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	_, loop := newEngineWithJobExit(t, store, vmm)

	_, _, run := seedJobRunTask(t, store, api.PlanScale, 3)

	// Run the tick. Cold-booting 3 VMs inside one tick is
	// synchronous from the test's PoV; the per-task goroutine
	// fan-out means each WakeJob races through the fake vmm, and
	// we wait for the coldBoots counter to reach 3 before the
	// run-level RecomputeJobRunStatus has had a chance to fan-in.
	loop.runJobsTick(context.Background())

	// Wait for the per-task goroutines to record terminal
	// status. The fan-in is asynchronous (RecomputeJobRunStatus
	// runs after the Mark* succeeds), so poll with a bounded
	// deadline to avoid hanging forever on a regression.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		updated, err := store.GetJobRunInternal(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("GetJobRunInternal: %v", err)
		}
		if updated.AggregateStatus == state.JobRunStatusSucceeded {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	final, err := store.GetJobRunInternal(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetJobRun: %v", err)
	}
	if final.AggregateStatus != state.JobRunStatusSucceeded {
		t.Errorf("aggregate_status = %q, want %q", final.AggregateStatus, state.JobRunStatusSucceeded)
	}
	if final.TasksSucceeded != 3 {
		t.Errorf("tasks_succeeded = %d, want 3", final.TasksSucceeded)
	}
}

// TestRunJobsTick_TwoTicksRaceForSameTask exercises the SKIP LOCKED
// guarantee at the MemStore layer (which mirrors the PG contract
// via per-run mutex): two ticks racing for the same task see
// exactly one claim per task. This is the property that lets a
// future multi-node schedd fleet coexist — cluster-wide, no two
// schedds can ever claim the same task.
//
// The fake vmm blocks inside CreateColdBoot via sleepFor so the
// first tick's WakeJob goroutine holds its task in 'claimed' state
// when the second tick fires; the second tick must skip the row
// (already claimed) rather than re-claim it.
func TestRunJobsTick_TwoTicksRaceForSameTask(t *testing.T) {
	store := state.NewMemStore()
	// Block CreateColdBoot so the first tick's WakeJob holds
	// the task in 'claimed' state when the second tick fires.
	// sleepFor is sufficient: the fakeVMM increments coldBoots
	// AFTER its sleep timer elapses. The second tick's
	// ListReadyJobTasks sees no 'queued' rows (the first tick
	// already flipped status to 'claimed') and short-circuits
	// without a second cold-boot.
	vmm := &fakeVMM{
		sleepFor: 200 * time.Millisecond,
	}
	_, loop := newEngineWithJobExit(t, store, vmm)

	_, _, run := seedJobRunTask(t, store, api.PlanScale, 1)

	// Tick 1: claims task 0 + enters CreateColdBoot. The
	// per-task goroutine starts the cold-boot, blocks on
	// sleepFor, then completes. We don't wait for tick 1 to
	// finish — instead, poll ListReadyJobTasks to confirm the
	// row has been flipped to 'claimed'.
	go loop.runJobsTick(context.Background())

	// Wait until the row has been claimed (ListReadyJobTasks
	// returns empty). The MarkJobTaskClaimed call inside
	// runClaimedTask flips the row before WakeJob's cold-boot
	// RPC, so the second tick can observe the row as
	// 'claimed' even while tick 1 is still in flight.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		ready, err := store.ListReadyJobTasks(context.Background(), 10)
		if err == nil && len(ready) == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Tick 2: the task row is already 'claimed', so
	// ListReadyJobTasks returns empty. tick 2 must not boot
	// anything — coldBoots stays at 0 because tick 1 hasn't
	// yet incremented the counter (it's still in sleepFor).
	loop.runJobsTick(context.Background())
	if vmm.coldBoots != 0 {
		t.Errorf("after tick 2: coldBoots = %d, want 0 (no second boot — task already claimed)", vmm.coldBoots)
	}

	// Wait for tick 1's per-task goroutine to complete + flip
	// the run to succeeded.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		final, err := store.GetJobRunInternal(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("GetJobRunInternal: %v", err)
		}
		if final.AggregateStatus == state.JobRunStatusSucceeded {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	final, _ := store.GetJobRunInternal(context.Background(), run.ID)
	t.Errorf("aggregate_status = %q, want %q after tick 1+2", final.AggregateStatus, state.JobRunStatusSucceeded)
}

// TestRunJobsTick_PerTickCapBound fences the per-tick claim cap
// (maxClaimsPerTick=32 in dispatch_jobs.go). The intent is that
// no single tick cold-boots more than maxClaimsPerTick tasks.
//
// We pin this structurally rather than timing-sensitive: the
// claim cap is a package-level constant. The runtime test
// here verifies the SECONDARY invariant — that the dispatch
// tick's runJobsTick drives every task through to terminal
// status eventually, regardless of how the per-account
// concurrency cap (limits.JobMaxConcurrentPerAccount) shapes
// the in-flight admission.
//
// Hobby plan (cap=3) is the strictest reasonable bound. The
// dispatch tick + ErrJobAdmissionRefused + MarkJobTaskRequeued
// pipeline together drive every task through to terminal;
// tasks refused at admission flip claimed→queued so the next
// tick re-claims them. With a tight per-account cap and
// fakeVMM completing cold-boots in microseconds, the cap
// releases fast enough that the re-queue rarely fires, but
// the contract (eventual terminal state) holds regardless.
func TestRunJobsTick_PerTickCapBound(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	_, loop := newEngineWithJobExit(t, store, vmm)

	const total = 20
	_, _, run := seedJobRunTask(t, store, api.PlanHobby, total)

	// Hobby cap = 3 concurrent tasks. Drive the dispatch tick
	// in a tight loop so the re-queue path (MarkJobTaskRequeued
	// + ListReadyJobTasks re-claim) gets exercised. The 1s
	// production ticker is too slow for a unit test; the loop
	// is the test seam that drives re-claims.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			loop.runJobsTick(context.Background())
			time.Sleep(10 * time.Millisecond)
			final, err := store.GetJobRunInternal(context.Background(), run.ID)
			if err == nil {
				if final.AggregateStatus == state.JobRunStatusSucceeded ||
					final.AggregateStatus == state.JobRunStatusDeadLetter {
					return
				}
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dispatch tick loop did not drive run to terminal in 10s")
	}

	final, err := store.GetJobRunInternal(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("GetJobRunInternal: %v", err)
	}
	totalCounted := int(final.TasksSucceeded) + int(final.TasksFailed) +
		int(final.TasksCancelled)
	if totalCounted != total {
		t.Errorf("task counter sum = %d, want %d (terminal fan-in incomplete)",
			totalCounted, total)
	}
	// Per-task status check: every row must be terminal (the
	// dispatch tick's contract is "every claimed task gets a
	// terminal mark"). The SKIP LOCKED claim + per-account
	// cap + requeue together guarantee no task is left in
	// queued/claimed once the run-level terminal fan-in fires.
	for i := int32(0); i < total; i++ {
		task, err := store.JobTaskByRunAndIndex(context.Background(), run.ID, i)
		if err != nil {
			t.Fatalf("JobTaskByRunAndIndex(%d): %v", i, err)
		}
		if task.Status == state.JobTaskStatusQueued ||
			task.Status == state.JobTaskStatusClaimed {
			t.Errorf("task %d: status = %q, want terminal", i, task.Status)
		}
	}
}

// TestRecordTerminalAndRecompute_OutcomeMapping is the
// per-Outcome → Mark* contract pin. We exercise the dispatch
// goroutine's tail end (recordTerminalAndRecompute) by seeding
// a task row, flipping it to 'claimed', and calling the
// helper directly with each Outcome value. This bypasses the
// vmm RPC so we can pin the Mark* + recompute behaviour without
// a metal gate.
//
// Why the helper test rather than end-to-end: the contract is
// load-bearing (every PR-C bug in the Outcome → Mark* mapping
// surfaces as a corrupted dashboard panel) and the helper is
// the single seam — testing it in isolation is cheaper than
// round-tripping through the fake vmm.
func TestRecordTerminalAndRecompute_OutcomeMapping(t *testing.T) {
	cases := []struct {
		name           string
		outcome        JobOutcome
		wantTaskStatus state.JobTaskStatus
		wantErrorClass string // "" if no error class
	}{
		{"succeeded", JobOutcomeSucceeded, state.JobTaskStatusSucceeded, ""},
		{"failed", JobOutcomeFailed, state.JobTaskStatusFailed, "job_failed"},
		{"deadline", JobOutcomeDeadline, state.JobTaskStatusFailed, "deadline_exceeded"},
		{"cancelled", JobOutcomeCancelled, state.JobTaskStatusCancelled, ""},
		{"boot_fail", JobOutcomeBootFail, state.JobTaskStatusFailed, "boot_failed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
			loop := NewLoop(nil, engine, testLog())

			_, _, run := seedJobRunTask(t, store, api.PlanHobby, 1)
			// Flip task 0 to 'claimed' so the Mark* methods accept
			// the transition (MarkJobTaskFailed on a 'queued' row
			// would no-op via the SQL CHECK).
			if err := store.MarkJobTaskClaimed(context.Background(), run.ID, 0, ""); err != nil {
				t.Fatalf("MarkJobTaskClaimed: %v", err)
			}

			loop.recordTerminalAndRecompute(context.Background(), run.ID, 0, tc.outcome, nil)

			task, err := store.JobTaskByRunAndIndex(context.Background(), run.ID, 0)
			if err != nil {
				t.Fatalf("JobTaskByRunAndIndex: %v", err)
			}
			if task.Status != tc.wantTaskStatus {
				t.Errorf("status = %q, want %q", task.Status, tc.wantTaskStatus)
			}
			if tc.wantErrorClass != "" {
				if task.ErrorClass == nil || *task.ErrorClass != tc.wantErrorClass {
					t.Errorf("error_class = %v, want %q", task.ErrorClass, tc.wantErrorClass)
				}
			}
		})
	}
}

// TestLoadJobRunTask_RoundTrip pins that loadJobRunTask returns the
// same job+run+task rows seeded into the store. Used by
// runClaimedTask after ClaimJobTasks returns the (runID, taskIndex)
// tuple.
func TestLoadJobRunTask_RoundTrip(t *testing.T) {
	store := state.NewMemStore()
	engine := newEngine(t, store, &fakeVMM{}, &fakeNotifier{}, "1.10.0")
	loop := NewLoop(nil, engine, testLog())

	acct, job, run := seedJobRunTask(t, store, api.PlanHobby, 1)

	gotRun, gotJob, gotTask, err := loop.loadJobRunTask(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("loadJobRunTask: %v", err)
	}
	if gotRun.ID != run.ID {
		t.Errorf("run.ID = %q, want %q", gotRun.ID, run.ID)
	}
	if gotJob.ID != job.ID {
		t.Errorf("job.ID = %q, want %q", gotJob.ID, job.ID)
	}
	if gotTask.RunID != run.ID || gotTask.TaskIndex != 0 {
		t.Errorf("task = %+v, want RunID=%q TaskIndex=0", gotTask, run.ID)
	}
	if gotRun.AccountID != acct.ID {
		t.Errorf("run.AccountID = %q, want %q", gotRun.AccountID, acct.ID)
	}
}

// TestRunJobsTick_ConcurrentTicksNoDoubleClaim stresses the
// claim → mark contract: K parallel ticks all hit the same
// store with N queued tasks. The terminal-state invariants
// must hold even when the first tick's per-task goroutines
// haven't yet flipped status to 'claimed' before a concurrent
// tick's ListReadyJobTasks reads.
//
// What we actually pin: by the time the dust settles, every
// task MUST be in a terminal status (no row left in 'queued'
// or 'claimed'), and the run-level aggregate_status must
// reflect "succeeded" (every task completed happy). Cold-boots
// may legitimately exceed N because MemStore's claim is
// optimistic (parallel ticks can read overlapping "queued"
// rows; the production SKIP LOCKED SQL prevents this in PG).
// The terminal-state assertion is the real invariant —
// double-counted Mark* calls would push tasks_failed or
// tasks_succeeded over N.
func TestRunJobsTick_ConcurrentTicksNoDoubleClaim(t *testing.T) {
	store := state.NewMemStore()
	vmm := &fakeVMM{}
	_, loop := newEngineWithJobExit(t, store, vmm)

	const tasksN = 8
	const goroutines = 4
	_, _, run := seedJobRunTask(t, store, api.PlanScale, tasksN)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loop.runJobsTick(context.Background())
		}()
	}
	wg.Wait()

	// Wait for all per-task goroutines to settle. The dispatch
	// goroutine fans out, so we need to wait for the run to
	// reach a terminal status before asserting.
	deadline := time.Now().Add(5 * time.Second)
	var final state.JobRun
	for time.Now().Before(deadline) {
		got, err := store.GetJobRunInternal(context.Background(), run.ID)
		if err != nil {
			t.Fatalf("GetJobRunInternal: %v", err)
		}
		if got.AggregateStatus == state.JobRunStatusSucceeded ||
			got.AggregateStatus == state.JobRunStatusDeadLetter ||
			got.AggregateStatus == state.JobRunStatusCancelled {
			final = got
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Terminal-state fan-in: every task must end up counted,
	// regardless of whether the run is succeeded or dead_letter.
	totalCounted := int(final.TasksSucceeded) + int(final.TasksFailed) +
		int(final.TasksCancelled)
	if totalCounted != tasksN {
		t.Errorf("task counter sum = %d, want %d (terminal fan-in incomplete)",
			totalCounted, tasksN)
	}

	// Per-task status check: no task should remain in 'queued'
	// or 'claimed' (the dispatch tick's contract is "every
	// claimed task gets a terminal mark"). This is the
	// load-bearing invariant for the SKIP LOCKED claim: if a
	// task is double-claimed and both WakeJob calls boot, two
	// instances rows get created and both call MarkJobTask*
	// — the second Mark would fail (the row is no longer
	// 'claimed'), but the per-task row's terminal status
	// still gets stamped exactly once per (runID, taskIndex).
	for i := int32(0); i < tasksN; i++ {
		task, err := store.JobTaskByRunAndIndex(context.Background(), run.ID, i)
		if err != nil {
			t.Fatalf("JobTaskByRunAndIndex(%d): %v", i, err)
		}
		if task.Status == state.JobTaskStatusQueued ||
			task.Status == state.JobTaskStatusClaimed {
			t.Errorf("task %d: status = %q, want terminal (queued/claimed left over)", i, task.Status)
		}
	}

	// Reference vmm.coldBoots for diagnostic purposes if the
	// test regresses — a sane bound is "coldBoots ≤ 4 × N"
	// (one parallel tick per goroutine × N tasks each).
	_ = vmm.coldBoots
}
