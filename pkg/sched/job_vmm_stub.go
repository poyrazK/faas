// Package sched — legacy fail-open JobVMMClient adapter.
//
// The production schedd path uses the real VMMRouter now. This adapter remains
// as an explicit compatibility seam for tests or operators that intentionally
// want job dispatch to fail closed.

package sched

import (
	"context"
	"errors"
	"log/slog"
)

// ErrJobVMMNotWired is returned by FailOpenJobVMMClient.JobColdBoot. Stable sentinel
// for errors.Is checks in cmd/apid/handlers_jobs.go + the
// WakeJob error-classification branch in pkg/sched/jobs.go.
var ErrJobVMMNotWired = errors.New("sched: job vmm gRPC surface not wired (Mega-1 follow-up)")

// ErrJobLeaserNil is returned by Engine.WakeJob when a job leaser is absent.
// Stable sentinel — the compatibility path fails closed rather than panics.
var ErrJobLeaserNil = errors.New("sched: job leaser not wired (Mega-1 follow-up)")

// FailOpenJobVMMClient is the no-op jobVmmClient compatibility adapter.
// Every call returns (zero-value, ErrJobVMMNotWired). The engine
// catches the sentinel and routes the run to status='failed'
// (terminal, retryable) so the customer sees a CodeJobVMMUnavailable
// 503 on POST /v1/jobs/{name}/runs, not a nil-panic or a silent
// lease-loss loop.
//
// Why fail-open instead of nil (which would nil-deref in
// pkg/sched/jobs.go:158):
//
//   - nil-deref masks the issue behind a "wake panic" stack,
//     which is impossible to root-cause without a debugger
//     attached.
//   - A sentinel error flows through dispatchJobsTick's existing
//     classify-or-retry path, surfaces in `usage_daily` as a
//     dead-letter row, and pings the `jobs_dispatch_rejected`
//     Prometheus counter so the operator sees the dispatch is
//     turned off in observability.
type FailOpenJobVMMClient struct {
	log *slog.Logger
}

// NewFailOpenJobVMMClient returns a no-op jobVmmClient that surfaces
// ErrJobVMMNotWired on every JobColdBoot call.
func NewFailOpenJobVMMClient(log *slog.Logger) *FailOpenJobVMMClient {
	if log == nil {
		log = slog.Default()
	}
	return &FailOpenJobVMMClient{log: log}
}

// JobColdBoot satisfies jobVmmClient.
// Returns ErrJobVMMNotWired so the engine can classify the run as
// failed → retryable. Logs ONCE per (runID, taskIndex) pair via
// the engine's per-task de-dupe to avoid log floods during the
// dispatch tick.
func (c *FailOpenJobVMMClient) JobColdBoot(ctx context.Context, spec JobVmmSpec) (JobVmmResult, error) {
	c.log.Warn("schedd: JobColdBoot invoked but vmmd gRPC not wired yet",
		"run_id", spec.RunID,
		"task_index", spec.TaskIndex,
		"account_id", spec.AccountID,
		"hint", "use the real vmmd JobColdBoot client")
	return JobVmmResult{}, ErrJobVMMNotWired
}
