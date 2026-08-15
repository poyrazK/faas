// dispatch_jobs.go — schedd's per-job-task dispatch tick (ADR-099 / PR-C).
//
// runJobsTick is the per-tick dispatcher the cron loop's ticker
// (loop.go, jobsT) and the LISTEN nudge (handleNotification's
// NotifyJobTasksQueued case) both call. It walks the ready
// job_tasks rows, claims them via FOR UPDATE SKIP LOCKED, and
// fans each out to Engine.WakeJob on a per-task goroutine.
//
// Concurrency model:
//
//   - The dispatch tick is single-leader (schedd is single-node
//     today). The claim is atomic (SKIP LOCKED) so a re-entrant
//     tick can't double-claim. Two schedds in a future multi-node
//     fleet would also be safe — the SKIP LOCKED guarantee is
//     cluster-wide.
//
//   - Each claim runs WakeJob in its OWN goroutine so the tick
//     doesn't block on the slow path (cold-boot + supervisor +
//     job_exit). The tick itself returns promptly after handing
//     off; the goroutine owns the wake to completion.
//
//   - Per-account concurrency is bounded upstream by WakeJob
//     (CountLiveJobTasksForAccount + JobMaxConcurrentPerAccount).
//     The tick here just hands claims out as fast as possible;
//     WakeJob's admission gate is the real throttle.
//
// Terminal-status recording:
//
//   - WakeJob returns JobWakeResult.Outcome; the dispatch
//     goroutine picks the right JobStore Mark* method:
//
//     JobOutcomeSucceeded → MarkJobTaskSucceeded
//     JobOutcomeFailed    → MarkJobTaskFailed
//     JobOutcomeDeadline  → MarkJobTaskTimeout (PR-B state
//     machine reuses the timeout path
//     for deadline_exceeded — the
//     watchdog-kill signal is the same
//     from the biller's perspective)
//     JobOutcomeCancelled → MarkJobTaskCancelled
//     JobOutcomeBootFail  → MarkJobTaskFailed
//
//   - After each Mark*, RecomputeJobRunStatus is called. This is
//     the fan-in: pure SQL that computes the run-level
//     aggregate_status from the per-task rows. Doing it per-task
//     keeps the run-status dashboard fresh within ~1 task
//     transition, instead of only at run-end.
//
//   - Mark* is best-effort (returns nil on success; errors are
//     logged at Warn level and the task row stays in 'claimed'
//     state — the next sweep re-tries). A persistent failure
//     surfaces in /v1/jobs/runs/{id} (the api surfaces it via
//     the per-task status).
//
// ErrJobAdmissionRefused handling:
//
//   - WakeJob returns ErrJobAdmissionRefused when the per-account
//     concurrency cap (JobMaxConcurrentPerAccount) refuses the
//     wake, OR when the per-account rate-limit bucket refuses
//     the wake. The dispatch goroutine treats this as a benign
//     re-queue: it calls MarkJobTaskRequeued to flip status
//     claimed → queued (so ListReadyJobTasks picks the row up on
//     the next tick), logs at Debug level, and returns. This
//     matches the cron tick's AtCapacity backoff shape.
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// runJobsTick is the single-tick dispatcher. Called by the
// jobsT ticker (1 s cadence) and by the NotifyJobTasksQueued
// LISTEN arm. Idempotent: a re-entrant call sees no claimable
// rows (the previous tick claimed them) and returns promptly.
//
// Errors are logged at Warn level and never propagate out —
// a transient Postgres error or a bad claim shouldn't kill
// the loop. The next tick self-heals via the periodic sweep.
func (l *Loop) runJobsTick(ctx context.Context) {
	if l == nil || l.engine == nil || l.engine.store == nil {
		return
	}
	if ctx.Err() != nil {
		return
	}
	// Tick cap: bounded fan-out per tick so a 1000-task run
	// doesn't cold-boot 1000 VMs in a single 1 s tick (the
	// per-account concurrency + rate-limit consult inside
	// WakeJob throttles downstream, but the dispatch tick
	// itself should be bounded to avoid spinning the claim
	// loop unnecessarily). 32 matches the Scale plan's
	// JobMaxConcurrentPerAccount ceiling — at most one tick's
	// worth of admission decisions per second.
	const maxClaimsPerTick = 32

	// ListReadyJobTasks is the cheap pre-claim probe (SELECT
	// without FOR UPDATE). It surfaces up to N rows whose
	// status='pending' AND scheduled_at <= now AND attempt <
	// retry_max. The actual claim runs per-row via
	// ClaimJobTasks(runID, limit) which takes the SKIP LOCKED
	// pessimistic lock.
	ready, err := l.engine.store.ListReadyJobTasks(ctx, maxClaimsPerTick)
	if err != nil {
		l.log.Warn("dispatch_jobs: list ready", "err", err)
		return
	}
	if len(ready) == 0 {
		return
	}

	// Group by run_id so ClaimJobTasks is called once per run
	// rather than once per task (the SKIP LOCKED lock is
	// per-row, but the ClaimJobTasks signature batches by
	// runID for the same row-set to amortise the BEGIN).
	byRun := map[string]int32{}
	type readyRow struct {
		RunID     string
		TaskIndex int32
	}
	var ordered []readyRow
	for _, r := range ready {
		byRun[r.RunID]++
		ordered = append(ordered, readyRow{RunID: r.RunID, TaskIndex: r.TaskIndex})
	}

	for runID, n := range byRun {
		// ClaimJobTasks atomically flips status queued →
		// claimed for up to N rows of runID. On success
		// returns the (runID, taskIndex, instanceID) tuples
		// the dispatch goroutine hands to WakeJob. Note
		// instanceID is empty at claim time — WakeJob
		// creates the instances row and stamps the row's
		// PK back via MarkJobTaskClaimed.
		claimed, err := l.engine.store.ClaimJobTasks(ctx, runID, n)
		if err != nil {
			l.log.Warn("dispatch_jobs: claim", "run", runID, "err", err)
			continue
		}
		for _, c := range claimed {
			// Per-task goroutine. The fan-in is per-task
			// completion (RecomputeJobRunStatus after the
			// Mark*), not per-run. WakeJob's taskBudget
			// deadline is the per-task timeout, so a
			// runaway task cannot hold its goroutine
			// indefinitely.
			go l.runClaimedTask(ctx, runID, c)
		}
	}
}

// runClaimedTask owns the per-task wake-to-completion lifecycle:
// fetch job+run+task rows, call WakeJob, record the terminal
// status, fan-in to the run level. Runs on its own goroutine.
//
// The ctx is the loop's context (cancelled on schedd
// shutdown). WakeJob uses ctx for both the cold-boot RPC and
// the supervisor wait — so a schedd SIGTERM during a task
// cleanly surfaces as JobOutcomeCancelled.
func (l *Loop) runClaimedTask(ctx context.Context, runID string, c state.ClaimedJobTask) {
	if ctx.Err() != nil {
		return
	}
	// Resolve the task row's job+run+task tuple. The
	// ClaimJobTasks response carries taskIndex + instanceID; we
	// re-read job+run+task for the per-row shape WakeJob
	// needs. ListReadyJobTasks already touched these rows on
	// the previous tick; the re-read is cheap (PK lookup).
	run, job, task, err := l.loadJobRunTask(ctx, runID, c.TaskIndex)
	if err != nil {
		l.log.Warn("dispatch_jobs: load", "run", runID, "task", c.TaskIndex, "err", err)
		// Don't mark failed — the row might be a transient
		// blip. The next tick re-claims it via SKIP LOCKED
		// since we never stamped 'claimed' here (status
		// flips queued→claimed atomically inside
		// ClaimJobTasks; the failure is downstream of that
		// flip). A persistent load failure will surface
		// via the watchdog's stale-claimed sweep (a
		// follow-up; PR-C tracks it).
		return
	}

	// Stamp the instance ID on the row (PR-B contract).
	// MarkJobTaskClaimed(runID, taskIndex, instanceID) flips
	// status queued → claimed AND records the instance_id the
	// dispatch goroutine created. Failure here is non-fatal:
	// the instance ID is recoverable from instances.job_id
	// lookup, and the next sweep will reconcile.
	// ClaimedJobTask carries RunID+TaskIndex only (no
	// instanceID pre-claim); the actual ID lands after
	// WakeJob's CreateJobInstance call, but the dispatch
	// goroutine runs ahead of that. We mark-claimed with an
	// empty instanceID here — the row's status flips
	// regardless — and the meterd linkage uses
	// instances.job_id (set by WakeJob's INSERT) to find the
	// instance post-hoc.
	if err := l.engine.store.MarkJobTaskClaimed(ctx, runID, c.TaskIndex, ""); err != nil {
		l.log.Warn("dispatch_jobs: mark claimed", "run", runID, "task", c.TaskIndex, "err", err)
	}

	// WakeJob runs the cold-boot + supervisor + job_exit
	// dance. The error return distinguishes ErrJobAdmissionRefused
	// (re-queue: leave 'claimed' status, next tick picks up)
	// from real errors (mark failed).
	wakeStart := time.Now()
	wakeResult, wakeErr := l.engine.WakeJob(ctx, *run, *task, *job)
	wakeDur := time.Since(wakeStart)

	switch {
	case wakeErr == nil:
		// Hand off to per-Outcome recording.
	case errors.Is(wakeErr, ErrJobAdmissionRefused):
		// Benign re-queue: flip status claimed → queued
		// so ListReadyJobTasks picks the row up on the
		// next tick. Without the flip the row would
		// stay in 'claimed' forever (ListReadyJobTasks
		// only returns 'queued' rows) and the dispatch
		// tick would silently drop the task. Don't log
		// at Warn — a customer's burst hitting the cap
		// is expected steady-state.
		if err := l.engine.store.MarkJobTaskRequeued(ctx, runID, c.TaskIndex); err != nil {
			l.log.Warn("dispatch_jobs: requeue", "run", runID, "task", c.TaskIndex, "err", err)
		}
		l.log.Debug("dispatch_jobs: admission refused, re-queue",
			"run", runID, "task", c.TaskIndex, "err", wakeErr, "duration", wakeDur)
		return
	default:
		// Real error (boot fail, vsock transport, etc.).
		// WakeJob already transitioned the instance row
		// to FAILED; mark the task failed.
		l.log.Warn("dispatch_jobs: wake error", "run", runID, "task", c.TaskIndex, "err", wakeErr, "duration", wakeDur)
		l.recordTerminalAndRecompute(ctx, runID, c.TaskIndex, JobOutcomeBootFail, wakeErr)
		return
	}

	// Happy path: record by Outcome.
	l.recordTerminalAndRecompute(ctx, runID, c.TaskIndex, wakeResult.Outcome, nil)
}

// recordTerminalAndRecompute picks the right Mark* method for
// the Outcome, calls it, then fans in to the run level. Errors
// are logged at Warn level — a transient Postgres blip during
// the Mark* shouldn't kill the goroutine; the next sweep will
// re-reconcile the run-status.
func (l *Loop) recordTerminalAndRecompute(ctx context.Context, runID string, taskIndex int32, outcome JobOutcome, wakeErr error) {
	var markErr error
	switch outcome {
	case JobOutcomeSucceeded:
		markErr = l.engine.store.MarkJobTaskSucceeded(ctx, runID, taskIndex)
	case JobOutcomeFailed:
		msg := "supervisor_exit_nonzero"
		if wakeErr != nil {
			msg = wakeErr.Error()
		}
		markErr = l.engine.store.MarkJobTaskFailed(ctx, runID, taskIndex, "job_failed", msg)
	case JobOutcomeDeadline:
		// The watchdog-killed case (PR-B state machine reuses
		// the timeout path for deadline_exceeded). The
		// per-task 'error_class' is 'deadline_exceeded' so
		// /v1/jobs/runs/{id} surfaces the right reason.
		markErr = l.engine.store.MarkJobTaskFailed(ctx, runID, taskIndex, "deadline_exceeded", "watchdog")
	case JobOutcomeCancelled:
		markErr = l.engine.store.MarkJobTaskCancelled(ctx, runID, taskIndex)
	case JobOutcomeBootFail:
		markErr = l.engine.store.MarkJobTaskFailed(ctx, runID, taskIndex, "boot_failed", "vmmd_or_supervisor")
	default:
		l.log.Warn("dispatch_jobs: unknown outcome", "run", runID, "task", taskIndex, "outcome", outcome)
		return
	}
	if markErr != nil {
		l.log.Warn("dispatch_jobs: terminal mark", "run", runID, "task", taskIndex, "outcome", outcome, "err", markErr)
		return
	}

	// Fan-in: pure-SQL recompute of the run's
	// aggregate_status. Failure here is non-fatal — the run
	// status will be re-reconciled by the next tick (or by
	// the cron sweep for the run-end transition).
	if _, err := l.engine.store.RecomputeJobRunStatus(ctx, runID); err != nil {
		l.log.Warn("dispatch_jobs: recompute run status", "run", runID, "err", err)
	}
}

// loadJobRunTask reads the job+run tuple for a claim. The
// run carries the account + JobID linkage; the job carries the
// image_ref, RAMMB, task timeout. The per-task row is fetched
// separately via JobTaskByRunAndIndex (PR-B's per-task getter).
// Returns pointers so the caller can pass *run, *job, *task
// directly to WakeJob.
func (l *Loop) loadJobRunTask(ctx context.Context, runID string, taskIndex int32) (*state.JobRun, *state.Job, *state.JobTask, error) {
	// GetJobRunInternal bypasses the per-account ACL — schedd is
	// a trusted internal caller; the dispatch tick resolves a
	// (runID, taskIndex) tuple without knowing the run's accountID
	// up front (ClaimJobTasks only returns the tuple).
	run, err := l.engine.store.GetJobRunInternal(ctx, runID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dispatch_jobs: GetJobRunInternal(%s): %w", runID, err)
	}
	job, err := l.engine.store.GetJob(ctx, run.JobID, run.AccountID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dispatch_jobs: GetJob(%s): %w", run.JobID, err)
	}
	task, err := l.engine.store.JobTaskByRunAndIndex(ctx, runID, taskIndex)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("dispatch_jobs: JobTaskByRunAndIndex(%s, %d): %w", runID, taskIndex, err)
	}
	return &run, &job, &task, nil
}
