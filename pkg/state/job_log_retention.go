package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// JobLogRetentionWindow is how many days after a job run finishes its tasks'
// stored output is kept for accounts on Plan (ADR-20261102183152).
type JobLogRetentionWindow struct {
	Plan api.Plan
	Days int
}

// JobLogRetentionBatch bounds one ClearExpiredJobLogs call.
type JobLogRetentionBatch struct {
	Windows []JobLogRetentionWindow
	// FloorDays is the shortest window. It bounds the index scan and is the
	// window of an account whose plan is not in Windows.
	FloorDays int
	// MaxRuns caps the finished runs one call considers.
	MaxRuns int
	// MaxTasks caps the declared tasks of the runs one call clears; a call
	// always takes at least one due run, however many tasks it has.
	MaxTasks int
}

// JobLogRetentionResult counts what one ClearExpiredJobLogs call cleared.
type JobLogRetentionResult struct {
	Runs     int64
	Tasks    int64
	Attempts int64
	// More reports that due runs may remain after this call.
	More bool
}

// JobLogRetentionStore clears the stored stdout/stderr of job runs that
// finished longer ago than their account plan's log retention
// (ADR-20261102183152). Runs, tasks and attempts are kept with their statuses
// and exit codes; job_tasks.log_content and job_task_attempts.log_content are
// emptied and log_truncated set. Optional on Store; callers type-assert.
type JobLogRetentionStore interface {
	ClearExpiredJobLogs(ctx context.Context, batch JobLogRetentionBatch) (JobLogRetentionResult, error)
}

var _ JobLogRetentionStore = (*PgStore)(nil)

// ClearExpiredJobLogs runs one batch of the job log retention sweep.
func (s *PgStore) ClearExpiredJobLogs(ctx context.Context, batch JobLogRetentionBatch) (JobLogRetentionResult, error) {
	plans := make([]string, 0, len(batch.Windows))
	days := make([]int32, 0, len(batch.Windows))
	for _, w := range batch.Windows {
		plans = append(plans, string(w.Plan))
		days = append(days, int32(w.Days))
	}
	row, err := sqlc.New().ClearExpiredJobRunLogs(ctx, s.pool, sqlc.ClearExpiredJobRunLogsParams{
		FloorDays: int32(batch.FloorDays),
		PlanDays:  days,
		Plans:     plans,
		MaxRuns:   int32(batch.MaxRuns),
		MaxTasks:  int64(batch.MaxTasks),
	})
	if err != nil {
		return JobLogRetentionResult{}, fmt.Errorf("state: clear expired job logs: %w", err)
	}
	return JobLogRetentionResult{
		Runs: row.Runs, Tasks: row.Tasks, Attempts: row.Attempts,
		More: row.Candidates > 0 && (row.Candidates > row.Due || row.Candidates >= int64(batch.MaxRuns)),
	}, nil
}
