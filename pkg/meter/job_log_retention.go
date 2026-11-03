package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261102183152: a job run's stored stdout/stderr (job_tasks.log_content
// and the attempt journal's copy in job_task_attempts.log_content) follows the
// account plan's runtime log retention, Plan.LogArchiveRetentionDaysMax, which
// the log_events sweep applies too. The clock starts when the run finishes.

// JobLogRetentionInterval is the sweep cadence. Hourly keeps Free's one-day
// window from overrunning by more than an hour, as for log_events.
const JobLogRetentionInterval = time.Hour

// JobLogRetentionRunBatch caps the finished runs one batch considers. The
// batch clears whole runs until their declared tasks reach
// RetentionBatchSize, so a batch of one-task cron runs stays as cheap as one
// fan-out run.
const JobLogRetentionRunBatch = 1000

// JobLogsCleared counts the rows one sweep cleared.
type JobLogsCleared struct {
	Runs     int64
	Tasks    int64
	Attempts int64
}

// JobLogRetentionWindows returns each plan's job log window, its runtime log
// archive cap, and the shortest of them. The shortest bounds the scan and
// applies to an account whose plan is not listed, as the log_events sweep
// gives an unresolvable plan the shortest window.
func JobLogRetentionWindows() ([]state.JobLogRetentionWindow, int) {
	windows := make([]state.JobLogRetentionWindow, 0, len(api.Plans))
	floor := 0
	for _, plan := range api.Plans {
		days := plan.LogArchiveRetentionDaysMax()
		if days <= 0 {
			continue
		}
		windows = append(windows, state.JobLogRetentionWindow{Plan: plan, Days: days})
		if floor == 0 || days < floor {
			floor = days
		}
	}
	return windows, floor
}

// RetentionOnceJobLogs clears expired job logs in bounded batches, at most
// MaxRetentionBatches per call. Hitting the cap returns ErrRetentionBatchCap
// with the counts so far; the next tick resumes where this one stopped.
func RetentionOnceJobLogs(ctx context.Context, store state.JobLogRetentionStore) (JobLogsCleared, error) {
	var total JobLogsCleared
	windows, floor := JobLogRetentionWindows()
	if floor <= 0 {
		return total, errors.New("meter: no plan has a log retention window")
	}
	batch := state.JobLogRetentionBatch{
		Windows: windows, FloorDays: floor,
		MaxRuns: JobLogRetentionRunBatch, MaxTasks: RetentionBatchSize,
	}
	for i := 0; i < MaxRetentionBatches; i++ {
		got, err := store.ClearExpiredJobLogs(ctx, batch)
		if err != nil {
			return total, fmt.Errorf("job log retention (batch %d, runs cleared so far %d): %w", i, total.Runs, err)
		}
		total.Runs += got.Runs
		total.Tasks += got.Tasks
		total.Attempts += got.Attempts
		if !got.More {
			return total, nil
		}
	}
	return total, ErrRetentionBatchCap
}

// JobLogRetentionLoop sweeps at boot and every interval. observe owns the
// operator metrics; failures stay visible and retry on the next tick.
func JobLogRetentionLoop(ctx context.Context, store state.JobLogRetentionStore, interval time.Duration, log *slog.Logger, observe func(JobLogsCleared, error)) {
	if interval <= 0 {
		interval = JobLogRetentionInterval
	}
	run := func() {
		cleared, err := RetentionOnceJobLogs(ctx, store)
		if observe != nil {
			observe(cleared, err)
		}
		if log == nil {
			return
		}
		attrs := []any{"runs_cleared", cleared.Runs, "tasks_cleared", cleared.Tasks, "attempts_cleared", cleared.Attempts}
		switch {
		case err == nil:
			log.Info("job log retention tick ok", attrs...)
		case errors.Is(err, ErrRetentionBatchCap):
			log.Warn("job log retention hit batch cap; will resume next tick", append(attrs, "err", err)...)
		default:
			log.Error("job log retention failed", append(attrs, "err", err)...)
		}
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
