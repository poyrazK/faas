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
//   1. JobTaskMarkTerminal(timeout) atomically settles the row to
//      status='timeout' so JobRunRecompute can settle the aggregate.
//   2. SIGKILL/destroy via vmmd (M7) — the VM may still be alive if vmmd
//      survived the schedd death; this frees the tenant RAM slot.
//   3. Lease columns are cleared as part of MarkTaskTerminal.
//
// Idempotent: a reaper sweep racing with a healthy schedd's
// HandleJobExit will see the task transition to terminal and skip
// the MarkTerminal (the WHERE clause in JobTaskFindStuck restricts
// to status='claimed'). The "double-terminal" race resolves cleanly
// because JobTaskMarkTerminal is itself idempotent on the
// (status IN ('queued','claimed')) guard.

package sched

import (
	"context"
	"errors"
	"fmt"
	"time"
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
// of tasks reclaimed (successfully transitioned to status='timeout').
// Idempotent across calls; safe to invoke from multiple schedds
// because each call's UPDATE is guarded by status='claimed'.
//
// Wired into cmd/schedd/main.go's main loop on a 5s ticker
// alongside the cronLoop. Production deployments run exactly one
// schedd's reaper at a time per cluster; multi-schedd setups rely
// on the lease-token + status='claimed' guards to serialise — a
// second reaper seeing the same stuck task will see status='timeout'
// after the first reaper's UPDATE and skip.
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
		instanceID := ""
		if t.InstanceID != nil {
			instanceID = *t.InstanceID
		}
		nodeID := e.ownerNodeID
		if t.LastLeaseNode != nil && *t.LastLeaseNode != "" {
			nodeID = *t.LastLeaseNode
		}
		// The MarkTaskTerminal transition is the atomic "reclaim"
		// — once it commits, the lease_token / lease_expires_at
		// columns are cleared (per the UPDATE shape in
		// JobTaskMarkTerminal) so a concurrent reaper on a second
		// schedd sees status='timeout' and skips.
		//
		// exit_code=137 (OOM-killed or SIGKILL) + error_class=
		// 'timeout' is the canonical mapping for "reaper took
		// over"; HandleJobExit treats exit_code=137 + error_class
		// != 'oom' as failed→retry if budget remains. To force a
		// terminal-no-retry, we pass exit_code=124 (the
		// coreutils `timeout` sentinel) so mapExitToTerminalStatus
		// (jobs.go) lands on 'timeout'.
		if err := e.store.JobTaskMarkTerminal(ctx, t.RunID, t.TaskIndex, "timeout", 124, "infra", "reaper reclaimed stale lease", time.Now()); err != nil {
			// Likely the task already settled to a different
			// terminal status via HandleJobExit (lost race). Skip.
			continue
		}
		reclaimed++
		if t.LeaseToken != nil && e.jobLeaser != nil {
			if err := e.jobLeaser.Release(ctx, LeaseToken(*t.LeaseToken), nodeID); err != nil && !errors.Is(err, ErrLeaseNotFound) {
				e.log.Warn("sched: release reaped job lease", "run", t.RunID, "task", t.TaskIndex, "err", err)
			}
		}
		e.cleanupJobInstance(ctx, instanceID, nodeID, "job_reaper_timeout")
		// Settle the parent run's aggregate counters. Best-effort:
		// the next dispatch tick + a successful HandleJobExit will
		// also drive recompute; double-recompute is harmless.
		_, _ = e.store.JobRunRecompute(ctx, t.RunID)
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
	return e.ReapStuckJobTasks(ctx, StuckJobReaperConfig{})
}
