// Package sched — stuck-job-task reaper (issue #1184 Workstream A / ADR-099).
//
// The job-task reaper is structurally distinct from the app-instance
// idle reaper (pkg/sched/reaper.go). The idle reaper parks parked
// RUNNING instances after plan-idle-timeout; job tasks are NEVER
// parked (M5 dispatch invariant: every job_task runs to terminal in
// a single VM). What the job reaper DOES is reclaim tasks whose
// owning schedd has lost its lease — either the schedd crashed
// mid-boot or vmmd died before reporting the exit DGRAM.
//
// Sweep criteria: status='claimed' AND lease_expires_at IS NOT NULL
// AND lease_expires_at < now() - ttl. ttl is the grace window beyond
// the lease_expires_at; we keep it short (default 30s) so a crashed
// schedd's tasks reclaim quickly without false positives on a busy
// healthy schedd.
//
// On a match the reaper:
//   1. Fences the expired lease by token and expiry, then atomically queues
//      the next bounded retry or marks timeout and increments dead letter.
//   2. SIGKILL/destroy via vmmd (M7) — the VM may still be alive if vmmd
//      survived the schedd death; this frees the tenant RAM slot.
//   3. Lease columns are cleared as part of MarkTaskTerminal.
//
// Idempotent: a reaper sweep racing with a heartbeat or HandleJobExit
// loses the fenced transition and leaves the newer lease/result intact.

package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// StuckJobReaperConfig is the operator-tunable shape of the sweep.
// Defaults below match the per-tick overhead budget for a 5k
// task fleet.
type StuckJobReaperConfig struct {
	// TTL is the grace window beyond lease_expires_at. A task is
	// considered stuck iff lease_expires_at < now() - TTL. Default
	// 30s. Tighter (e.g. 5s) reclaims faster but risks false
	// positives on a schedd that's just slow to renew; looser
	// (e.g. 5min) is safer but holds tenant RAM longer after a
	// crash.
	TTL time.Duration

	// BatchSize caps the per-tick claim count so a 10k-stuck-task
	// backlog doesn't monopolise a schedd goroutine. Default 64.
	BatchSize int

	// IntervalSeconds is the per-tick period. Default 5s. Wired
	// from FAAS_JOB_REAPER_INTERVAL_SECONDS (env override).
	IntervalSeconds int
}

func (c StuckJobReaperConfig) withDefaults() StuckJobReaperConfig {
	if c.TTL <= 0 {
		c.TTL = 30 * time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 64
	}
	if c.IntervalSeconds <= 0 {
		c.IntervalSeconds = 5
	}
	return c
}

// ReapStuckJobTasks is one sweep of the reaper. Returns the number
// of tasks reclaimed (retried or transitioned to status='timeout').
// Idempotent across calls; safe to invoke from multiple schedds
// because each transition is guarded by status, token and expiry.
//
// Wired into cmd/schedd/main.go's main loop on a 5s ticker
// alongside the cronLoop. Production deployments run exactly one
// schedd's reaper at a time per cluster; multi-schedd setups rely
// on the lease-token + status='claimed' guards to serialise — a
// second reaper seeing the same stale receipt will skip after the first
// reaper clears the lease.
//
// Why a separate function (not a method on Engine): the reaper is
// invoked from the schedd main loop, not from the engine's per-wake
// critical section. Hoisting it out keeps engine.go focused and
// lets the reaper have its own error + metrics path.
func (e *Engine) ReapStuckJobTasks(ctx context.Context, cfg StuckJobReaperConfig) (int, error) {
	cfg = cfg.withDefaults()
	stuck, err := e.store.JobTaskFindStuck(ctx, cfg.TTL)
	if err != nil {
		return 0, fmt.Errorf("sched: reapStuckJobTasks find: %w", err)
	}
	if len(stuck) > cfg.BatchSize {
		stuck = stuck[:cfg.BatchSize]
	}
	reclaimed := 0
	for _, t := range stuck {
		if t.LeaseToken == nil {
			continue
		}
		run, err := e.store.JobRunGetByID(ctx, t.RunID)
		if err != nil {
			return reclaimed, fmt.Errorf("sched: reaper resolve run %s: %w", t.RunID, err)
		}
		job, err := e.store.JobGetByID(ctx, run.JobID)
		if err != nil {
			return reclaimed, fmt.Errorf("sched: reaper resolve job %s: %w", run.JobID, err)
		}
		instanceID := ""
		if t.InstanceID != nil {
			instanceID = *t.InstanceID
		}
		nodeID := e.ownerNodeID
		if t.LastLeaseNode != nil && *t.LastLeaseNode != "" {
			nodeID = *t.LastLeaseNode
		}
		_, err = e.store.JobTaskReapClaimed(ctx, t.RunID, t.TaskIndex, *t.LeaseToken,
			time.Now().Add(-cfg.TTL), effectiveJobRetryMax(job, run), time.Now().Add(jobRetryDelay(t.Attempt)))
		if errors.Is(err, state.ErrNotFound) {
			// A renewed lease or a competing terminal transition won.
			continue
		}
		if err != nil {
			return reclaimed, fmt.Errorf("sched: reap task (%s, %d): %w", t.RunID, t.TaskIndex, err)
		}
		reclaimed++
		if t.LeaseToken != nil && e.jobLeaser != nil {
			if err := e.jobLeaser.Release(ctx, LeaseToken(*t.LeaseToken), nodeID); err != nil && !errors.Is(err, ErrLeaseNotFound) {
				e.log.Warn("sched: release reaped job lease", "run", t.RunID, "task", t.TaskIndex, "err", err)
			}
		}
		e.cleanupJobInstance(ctx, instanceID, nodeID, "job_reaper_timeout")
	}
	return reclaimed, nil
}

// JobReaperTick returns the per-tick driver. The caller
// (cmd/schedd/main.go) wraps this in a time.Ticker loop:
//
//	ticker := time.NewTicker(5 * time.Second)
//	for {
//	    select {
//	    case <-ctx.Done(): return
//	    case <-ticker.C:
//	        if n, err := e.ReapStuckJobTasks(ctx, cfg); err != nil {
//	            log.Warn("reaper", "err", err)
//	        } else if n > 0 {
//	            log.Info("reaper reclaimed", "n", n)
//	        }
//	    }
//	}
//
// Distinct from a method to keep the reaper testable in isolation
// (pkg/sched/reaper_jobs_test.go will exercise ReapStuckJobTasks
// directly without driving a ticker).
func (e *Engine) JobReaperTick(ctx context.Context) (int, error) {
	cfg := StuckJobReaperConfig{}.withDefaults()
	reaped, reapErr := e.ReapStuckJobTasks(ctx, cfg)
	reconciled, reconcileErr := e.ReconcileOrphanedJobInstances(ctx, cfg.BatchSize)
	return reaped + reconciled, errors.Join(reapErr, reconcileErr)
}

// ReconcileOrphanedJobInstances retries the durable half of terminal job
// cleanup. A row is eligible when the instance is still live but no claimed
// task owns it. This includes terminal tasks whose first vmmd destroy failed
// and unbound rows left by older non-atomic dispatchers.
func (e *Engine) ReconcileOrphanedJobInstances(ctx context.Context, limit int) (int, error) {
	orphans, err := e.store.ListOrphanedJobInstances(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("sched: reconcile orphaned job instances: list: %w", err)
	}
	reconciled := 0
	var cleanupErrs []error
	for _, ins := range orphans {
		if e.ops != nil {
			e.ops.JobInstanceReconcileDecisions("found").Inc()
		}
		if e.vmm == nil {
			reconcileErr := fmt.Errorf("instance %s: vmm router unavailable", ins.ID)
			cleanupErrs = append(cleanupErrs, reconcileErr)
			if e.ops != nil {
				e.ops.JobInstanceReconcileDecisions("error").Inc()
			}
			continue
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DestroyTimeout)
		destroyErr := e.vmm.Destroy(cleanupCtx, e.nodeForRoute(ins.NodeID), ins.ID)
		if destroyErr != nil {
			cancel()
			cleanupErrs = append(cleanupErrs, fmt.Errorf("destroy instance %s: %w", ins.ID, destroyErr))
			if e.ops != nil {
				e.ops.JobInstanceReconcileDecisions("error").Inc()
			}
			e.log.Warn("sched: reconcile orphaned job instance: destroy", "instance", ins.ID, "node", ins.NodeID, "err", destroyErr)
			continue
		}
		e.settleJobInstance(cleanupCtx, ins.ID, "job_orphan_reconciled")
		cancel()
		if e.ops != nil {
			e.ops.JobInstanceReconcileDecisions("cleaned").Inc()
		}
		reconciled++
	}
	return reconciled, errors.Join(cleanupErrs...)
}
