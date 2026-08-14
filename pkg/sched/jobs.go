// jobs.go — schedd's per-job wake primitive (ADR-099 / PR-C).
//
// WakeJob is the engine entry point the dispatch tick calls once per
// claim. It is structurally a sibling of Wake (engine.go) but with
// three load-bearing differences:
//
//  1. cold-boot only. Job task VMs NEVER restore from snapshot — they
//     always cold-boot from the job's OCI image_ref. Snapshots are
//     cache, not truth (ADR-005); for jobs there is no parked
//     predecessor to restore from anyway. Skipping the warm tier
//     also closes the cold-boot-only invariant the reaper and
//     watchdog share (issue #470 follow-up).
//
//  2. No apps.last_scale_out_at stamp. The stamp drives the
//     scale-in cooldown consult on the wake gate (issue #462); job
//     tasks have no apps row to stamp — kind='job_task' rows have
//     app_id IS NULL by the instances_app_or_job_chk constraint
//     (migration 00256).
//
//  3. Ledger accounting goes through the per-account ram budget only
//     (no per-app / per-deployment). Jobs don't have per-deployment
//     RAM semantics; the wake path here reserves RAM on the chosen
//     compute_node and tracks the per-account concurrency against
//     JobMaxConcurrentPerAccount (limits.go). Failure to admit
//     surfaces as ErrJobAdmissionRefused, which the dispatch tick
//     re-queues into the next tick rather than failing the task —
//     burst pressure is expected to come and go within seconds.
//
// The terminal-status handoff arrives on vsock port 1026,
// msg_type=4 ("job_exit") — distinct from msg_type=3 (the
// characterization probe that wakes on the same port). The supervisor
// (guest/init/job_supervisor.go) sends a 32-byte little-endian
// {exit_code:u32, signal:u32, epoch_ns:u64} payload before
// poweroff. pkg/fcvm.WaitJobExit decodes the payload and returns
// (exitCode, signal, err). The deadline watchdog
// (pkg/sched/watchdog.go's jobDeadline branch) trips if the supervisor
// never sends within task.TaskTimeoutS + 30s grace; the task gets
// marked DeadlineExceeded (PR-B state machine).
package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// JobWakeResult is the typed return from WakeJob. Distinguishes the
// happy exit (Result.NonZero=false, Result.ExitCode=0) from
// non-zero exits and from the deadline / cancel / boot-fail paths.
// The dispatch tick records the (RunID, TaskIndex) → terminal-status
// transition via MarkJobTaskSucceeded / Failed / Cancelled / DeadlineExceeded
// (PR-B JobStore methods) keyed on Outcome.
type JobWakeResult struct {
	// InstanceID is the per-task VM's instances.id. Surfaced for
	// meterd usage_minutes linkage (meter_kind='job' + job_id +
	// instance_id rows). Empty on Outcome != ok.
	InstanceID string
	// NodeID is the compute_node the task VM lives on (matches
	// Instance.NodeID). Same empty-on-failure contract.
	NodeID string
	// ExitCode is the supervisor's reported exit (0..255). -1 when
	// the supervisor never reported (boot-fail or watchdog-killed).
	ExitCode int
	// Signal is the supervisor's reported signal number (POSIX
	// 1..31; 0 = no signal). -1 when the supervisor never reported.
	Signal int
	// Outcome is the closed-set terminal classification the
	// dispatch tick uses to pick the right Mark* method:
	//   - JobOutcomeSucceeded      (exit 0)
	//   - JobOutcomeFailed         (non-zero exit OR supervisor
	//                               reported a fatal signal)
	//   - JobOutcomeDeadline       (watchdog tripped; supervisor
	//                               didn't report within TaskTimeoutS)
	//   - JobOutcomeCancelled      (ctx cancelled mid-task)
	//   - JobOutcomeBootFailed     (vmmd RPC rejected; never booted)
	Outcome JobOutcome
}

// JobOutcome is the closed-set JobWakeResult.Outcome.
type JobOutcome string

const (
	JobOutcomeSucceeded JobOutcome = "succeeded"
	JobOutcomeFailed    JobOutcome = "failed"
	JobOutcomeDeadline  JobOutcome = "deadline_exceeded"
	JobOutcomeCancelled JobOutcome = "cancelled"
	JobOutcomeBootFail  JobOutcome = "boot_failed"
)

// ErrJobAdmissionRefused is returned by WakeJob when the per-account
// concurrency cap (limits.JobMaxConcurrentPerAccount) refuses the
// wake. The dispatch tick treats this as a benign re-queue signal —
// the task remains claimable on the next tick.
var ErrJobAdmissionRefused = errors.New("sched: job admission refused (per-account concurrency cap)")

// WakeJob runs one job task to completion: cold-boots a fresh
// Firecracker VM from the job's OCI image_ref, runs the task
// supervisor (guest/init/job_supervisor.go), and returns when the
// supervisor sends the job_exit DGRAM (port 1026, msg_type=4) OR
// the watchdog trips OR ctx is cancelled.
//
// The function is NOT safe for concurrent calls on the same
// taskID — the dispatch tick claims rows via FOR UPDATE SKIP LOCKED
// so two ticks cannot pick the same task; the dispatch tick is the
// single owner of a task row at any time. Concurrent calls on
// different task IDs ARE safe — each call's instance row is its own
// ledger entry, and the engine's per-app lock isn't taken (jobs
// have no app_id).
func (e *Engine) WakeJob(ctx context.Context, run state.JobRun, task state.JobTask, job state.Job) (JobWakeResult, error) {
	if e == nil {
		return JobWakeResult{}, errors.New("sched: WakeJob: nil engine")
	}
	if e.store == nil {
		return JobWakeResult{}, errors.New("sched: WakeJob: nil store")
	}
	if e.vmm == nil {
		return JobWakeResult{}, errors.New("sched: WakeJob: nil vmm client")
	}
	if run.ID == "" || task.RunID == "" || task.RunID != run.ID {
		return JobWakeResult{}, fmt.Errorf("sched: WakeJob: run/task id mismatch (run=%q task.RunID=%q)", run.ID, task.RunID)
	}
	if task.TaskIndex < 0 {
		return JobWakeResult{}, fmt.Errorf("sched: WakeJob: task index %d out of range", task.TaskIndex)
	}
	if job.ID == "" || job.ID != run.JobID {
		return JobWakeResult{}, fmt.Errorf("sched: WakeJob: job/run mismatch (job=%q run.JobID=%q)", job.ID, run.JobID)
	}

	// Resolve the account → plan → per-account cap. Missing account
	// surfaces as a benign boot-fail (the dispatch tick re-queues
	// and the next iteration will surface a typed error). cap=0
	// (Free plan) is short-circuited up-front: jobs are not part of
	// the Free plan and the API gate already returns CodeJobNotAllowed,
	// but the dispatch tick must not blow up if a Free-plan job row
	// leaked through (e.g. operator-only /v1/jobs/admin path).
	acct, acctErr := e.store.AccountByID(ctx, run.AccountID)
	if acctErr != nil {
		return JobWakeResult{Outcome: JobOutcomeBootFail}, fmt.Errorf("sched: WakeJob: account lookup %s: %w", run.AccountID, acctErr)
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return JobWakeResult{Outcome: JobOutcomeBootFail}, fmt.Errorf("sched: WakeJob: unknown plan %q", acct.Plan)
	}
	if limits.JobMaxConcurrentPerAccount <= 0 {
		return JobWakeResult{}, fmt.Errorf("%w: plan %q has no job concurrency budget", ErrJobAdmissionRefused, acct.Plan)
	}

	// Per-account rate-limit consult (PR-0 / ADR-099 Risk #1).
	// Separate from app wake buckets so a Scale customer's
	// `--tasks 1000 --parallelism 100` cannot drain the bucket the
	// customer's app wake path shares. nil-safe (the limiter is
	// optional in unit tests).
	if e.wakeLimiter != nil && !e.wakeLimiter.AllowWakeJobAccount(string(acct.ID)) {
		return JobWakeResult{}, fmt.Errorf("%w: per-account rate limit", ErrJobAdmissionRefused)
	}

	// Per-account concurrency consult. Counts the live instances
	// where kind='job_task' AND job_id IN (run's job). The partial
	// index instances_job_id_idx WHERE job_id IS NOT NULL is the
	// hot path; the count is O(log N) per claim rather than a
	// seq-scan. Pessimistic: if the cap is reached we surface
	// ErrJobAdmissionRefused and the dispatch tick re-queues.
	liveCount, liveErr := e.store.CountLiveJobTasksForAccount(ctx, string(acct.ID))
	if liveErr != nil {
		return JobWakeResult{Outcome: JobOutcomeBootFail}, fmt.Errorf("sched: WakeJob: live job-task count: %w", liveErr)
	}
	if int(liveCount) >= limits.JobMaxConcurrentPerAccount {
		return JobWakeResult{Outcome: JobOutcomeBootFail}, fmt.Errorf("%w: %d live >= cap %d", ErrJobAdmissionRefused, liveCount, limits.JobMaxConcurrentPerAccount)
	}

	// Resolve placement — single-box deployments degenerate to
	// default-local because there's only one active node. The
	// chooser reads active compute_nodes + per-node headroom, so
	// a fleet of N schedds with per-node quotas works unchanged.
	placement, plErr := e.choosePlacementLocked(ctx, Request{
		AppID:          "", // no app — jobs have no apps row
		Plan:           acct.Plan,
		RAMMB:          int(job.RamMb),
		VCPU:           limits.VCPU,
		MaxConcurrency: 1, // one wake per WakeJob call
	})
	if plErr != nil {
		// *api.Problem from chooser (capacity / no nodes).
		return JobWakeResult{Outcome: JobOutcomeBootFail}, fmt.Errorf("sched: WakeJob: placement: %w", plErr)
	}

	// Phase 2: CreateInstance row + ledger reservation.
	//
	// Cold-boot only — we ALWAYS start the VM from the OCI image_ref
	// (BaseKey = layerKey derived from the job's image_ref + a
	// per-task rootfs hash). Snapshots do not apply: jobs aren't
	// parked, the supervisor runs the task and exits.
	//
	// JobID is stamped on the row + Kind='job_task' (per migration
	// 00256's instances_app_or_job_chk pair-CHECK). app_id is the
	// empty string (the column is nullable post-00256; the CHECK
	// requires NULL for kind='job_task').
	wakeUUID, wuErr := uuid.NewV7()
	if wuErr != nil {
		// v4 fallback mirrors Wake's contract (gaps analysis 2026-07-23).
		wakeUUID = uuid.New()
		e.log.Warn("wakejob: uuid.NewV7 failed, fell back to v4",
			"run", run.ID, "task", task.TaskIndex, "err", wuErr)
	}
	wakeID := wakeUUID.String()

	// INSERT the instances row (kind='job_task', app_id=NULL,
	// job_id=run.JobID). Returns the row's UUID + the existing
	// Instance fields the dispatch tick will need for the
	// meterd linkage + post-boot bookkeeping.
	ins, insErr := e.store.CreateJobInstance(ctx, run.JobID, string(state.StateColdBooting), int(job.RamMb), placement.NodeID, wakeID)
	if insErr != nil {
		return JobWakeResult{Outcome: JobOutcomeBootFail}, fmt.Errorf("sched: WakeJob: create instance: %w", insErr)
	}

	// Ledger reservation. The chooser computed RAMMB+VCPUBudget
	// against the chosen node; ledger.Admit returns a Problem on
	// over-cap (CodeCapacity) which we surface as boot-fail.
	//
	// ADR-099 / PR-C: Kind=KindJob tells the ledger to skip the
	// per-app concurrency check — jobs have no app row, so the
	// per-app key collapses to "" and the ledger would refuse the
	// second concurrent task on an account. Per-node RAM + vCPU
	// ceilings still apply. The per-account concurrency cap is
	// enforced upstream by WakeJob's CountLiveJobTasksForAccount
	// consult (immediately above this block).
	if err := e.ledger.Admit(Request{
		Instance:       ins.ID,
		AppID:          "", // job path: no app row
		DeploymentID:   "", // job path: no deployment row
		Plan:           acct.Plan,
		RAMMB:          int(job.RamMb),
		VCPU:           limits.VCPU,
		MaxConcurrency: 1,
		Kind:           KindJob,
		NodeID:         placement.NodeID,
		NodeCeilingMB:  placement.CeilingMB,
		VCPUBudget:     placement.VCPUBudget,
	}); err != nil {
		// Roll back the unattached reservation; row never had a
		// running reservation attached.
		if delErr := e.store.DeleteInstance(ctx, ins.ID); delErr != nil {
			e.log.Warn("wakejob: delete unattached row after ledger refusal",
				"instance", ins.ID, "run", run.ID, "task", task.TaskIndex, "err", delErr)
		}
		return JobWakeResult{Outcome: JobOutcomeBootFail}, err
	}

	// Phase 3: drop the per-app lock (jobs have no app_id so we
	// don't take e.lockApp here — WakeJob never serialises on a
	// per-app mutex), do the slow vmmd RPC, wait for the supervisor.
	//
	// Job tasks have a per-task timeout budget (job.TaskTimeoutS
	// plus a 30s grace for the supervisor's tear-down). On
	// overrun, the watchdog (pkg/sched/watchdog.go's jobDeadline
	// branch) trips; the supervisor is killed via vmmd, and the
	// task is marked DeadlineExceeded. WakeJob's budget mirrors
	// the ColdBootTimeout scale (35s base + per-task timeout
	// slice) so a 5-minute task gets a 5m+35s deadline.
	taskBudget := time.Duration(job.TaskTimeoutS)*time.Second + ColdBootTimeout
	bootCtx, cancelBoot := context.WithTimeout(ctx, taskBudget)
	defer cancelBoot()

	// Build the AppSpec the supervisor needs. Job path has:
	//   - no env_secrets (job.EnvOverrides is plaintext only; the
	//     runner mounts them as OS env on init)
	//   - no app/api env (no app row)
	//   - the per-task /etc/job/task.env mount carries {JOB_ID,
	//     RUN_ID, TASK_INDEX, ATTEMPT, TASK_TIMEOUT_S, IMAGE_REF}.
	spec := AppSpec{
		BaseKey:         job.ImageRef, // OCI image digest; vmmd's Storage.Get resolves the layer chain
		LayerKey:        job.ImageRef + "@task=" + run.ID + "/" + fmt.Sprintf("%d", task.TaskIndex),
		VCPUCount:       int32(limits.VCPU),
		MemSizeMiB:      job.RamMb,
		EgressMbit:      int32(limits.EgressMbit),
		APIEnv:          nil, // jobs have no app/api env
		EgressAllowlist: nil, // jobs use the platform default egress policy; allowlist is per-app
		Runtime:         "job",
		// Port defaults to 0; the supervisor runs headless (no HTTP
		// listener) and sends job_exit via vsock. waitReady's TCP
		// probe is skipped via Port=0 (vmmd's wire contract).
		Port: 0,
	}

	// Phase 3a: cold-boot RPC.
	rpcStartedAt := time.Now().UTC()
	out, bootErr := e.vmm.CreateColdBoot(bootCtx, placement.NodeID, ins.ID, spec)
	if bootErr != nil {
		// Release + mark instance FAILED. Don't roll back the
		// row — the audit trail benefits from a FAILED instance
		// row with the task linkage (job_id) intact.
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, "", state.StateFailed, "job_boot_error", "vmm_boot_failed")
		return JobWakeResult{InstanceID: ins.ID, NodeID: placement.NodeID, Outcome: JobOutcomeBootFail}, bootErr
	}

	// SetInstanceRuntime stamps netns/host_ip/guest_uid on the row.
	// Failure here = booted but unrecordable. Destroy + release +
	// return boot-fail (matches Wake's contract).
	if err := e.store.SetInstanceRuntime(ctx, ins.ID, out.Netns, out.HostIP, int(out.LeaseUID)); err != nil {
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, "", state.StateFailed, "job_boot_error", "record_runtime_failed")
		return JobWakeResult{InstanceID: ins.ID, NodeID: placement.NodeID, Outcome: JobOutcomeBootFail}, fmt.Errorf("sched: WakeJob: record runtime: %w", err)
	}

	// Phase 3b: wait for the supervisor's job_exit DGRAM.
	// Deadline is the same taskBudget; on overrun, the supervisor
	// hasn't reported and we treat it as DeadlineExceeded (the
	// watchdog kills the VM, the dispatch tick records the
	// outcome on the next sweep). The waiter is the local-host
	// vsock UDS seam (LocalJobExitWaiter); nil means PR-C
	// disabled and we return BootFail so the dispatch tick
	// doesn't loop forever (matches the wakeLimiter nil pattern).
	if e.jobExitWaiter == nil {
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, "", state.StateFailed, "job_boot_error", "no_exit_waiter")
		return JobWakeResult{InstanceID: ins.ID, NodeID: placement.NodeID, Outcome: JobOutcomeBootFail}, errors.New("sched: WakeJob: jobExitWaiter not configured (FAAS_JOBS_DISABLED=1?)")
	}
	exitCode, signal, exitErr := e.jobExitWaiter.WaitJobExit(bootCtx, ins.ID, taskBudget-time.Since(rpcStartedAt))
	switch {
	case exitErr == nil:
		// Normal exit.
	case errors.Is(exitErr, context.DeadlineExceeded):
		// Watchdog tripped; kill the VM and mark DeadlineExceeded.
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, "", state.StateFailed, "job_deadline_exceeded", "watchdog")
		return JobWakeResult{InstanceID: ins.ID, NodeID: placement.NodeID, Outcome: JobOutcomeDeadline}, exitErr
	case errors.Is(exitErr, context.Canceled):
		// Caller cancelled (the dispatch tick shutdown or the run
		// was cancelled mid-task). Kill + mark cancelled.
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, "", state.StateFailed, "job_cancelled", "ctx_cancel")
		return JobWakeResult{InstanceID: ins.ID, NodeID: placement.NodeID, Outcome: JobOutcomeCancelled}, exitErr
	default:
		// Other error (vmmd unreachable, vsock transport glitch).
		e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)
		e.ledger.Release(ins.ID)
		e.transitionWithKind(ctx, ins.ID, "", state.StateFailed, "job_exit_error", "vmmd_transport")
		return JobWakeResult{InstanceID: ins.ID, NodeID: placement.NodeID, Outcome: JobOutcomeBootFail}, exitErr
	}

	// Phase 4: post-exit commit. transition RUNNING → STOPPED,
	// release the ledger reservation, destroy the VM.
	e.transitionWithKind(ctx, ins.ID, "", state.StateStopped, "job_exit", "supervisor_exit")
	e.ledger.Release(ins.ID)
	e.bestEffortDestroy(ctx, placement.NodeID, ins.ID)

	outcome := JobOutcomeSucceeded
	if exitCode != 0 || signal != 0 {
		outcome = JobOutcomeFailed
	}
	e.log.Info("wakejob: task complete",
		"run", run.ID, "task", task.TaskIndex, "instance", ins.ID,
		"node", placement.NodeID, "exit_code", exitCode, "signal", signal, "outcome", outcome)

	// Best-effort: notify the dispatch tick so the next sweep
	// picks up the terminal-status task without waiting for the
	// periodic stale-task sweep (the dispatch tick's SKIP LOCKED
	// pattern doesn't re-read IN_FLIGHT rows; the notify drives
	// the next tick).
	_ = e.notif.Notify(ctx, "job_tasks_queued", run.JobID)
	return JobWakeResult{
		InstanceID: ins.ID,
		NodeID:     placement.NodeID,
		ExitCode:   exitCode,
		Signal:     signal,
		Outcome:    outcome,
	}, nil
}
