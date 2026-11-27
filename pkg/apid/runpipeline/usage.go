package runpipeline

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type AttemptUsage struct {
	RunID, Step string
	Attempt     int
	Status      api.ExecutionStatus
	LedgerState string
	QueueTime   *time.Duration
	Execution   time.Duration
	OutputAt    *time.Time
	outputs     int
}

type UsageBreakdown struct {
	Attempts        []AttemptUsage `json:"-"`
	NewRuns         int            `json:"new_runs"`
	RetryRuns       int            `json:"retry_runs"`
	ReusedSteps     int            `json:"reused_steps"`
	SettledRuns     int            `json:"settled_runs"`
	PendingRuns     int            `json:"pending_runs"`
	UnavailableRuns int            `json:"unavailable_runs"`
	WallTimeMS      int64          `json:"wall_time_ms"`
	OutputBytes     int64          `json:"output_bytes"`
	OutputCount     int            `json:"output_count"`
}

func (u UsageBreakdown) ExecutionTime() time.Duration {
	return time.Duration(u.WallTimeMS) * time.Millisecond
}

// ReadUsage attributes each physical Run ID to its originating pipeline only.
// Terminal receipts preserve attempt lineage even after a Run ages out of storage.
func ReadUsage(ctx context.Context, store state.PipelineRunUsageStore, accountID string, receipt *api.ExecutionPipelineResponse) (UsageBreakdown, error) {
	if receipt == nil {
		return UsageBreakdown{}, nil
	}
	usage, ids := usageAttempts(receipt)
	var records map[string]state.RunUsageRecord
	var err error
	if store != nil && len(ids) != 0 {
		records, err = store.PipelineRunUsage(ctx, accountID, ids)
	}
	for i := range usage.Attempts {
		attempt := &usage.Attempts[i]
		if row, ok := records[attempt.RunID]; ok {
			settleAttempt(&usage, attempt, row)
		} else if !attempt.Status.Terminal() && receipt.Completion == nil {
			attempt.LedgerState = "Pending"
			usage.PendingRuns++
		} else {
			attempt.LedgerState = "Unavailable"
			usage.UnavailableRuns++
		}
	}
	return usage, err
}

func usageAttempts(receipt *api.ExecutionPipelineResponse) (UsageBreakdown, []string) {
	used := make(map[string]bool)
	stepsReused := make(map[string]bool)
	if receipt.Rerun != nil {
		for _, ref := range receipt.Rerun.Reused {
			used[ref.RunID] = true
			if receipt.Completion == nil {
				stepsReused[ref.Label] = true
			}
		}
	}
	if receipt.Completion != nil {
		for _, step := range receipt.Completion.Steps {
			if step.ReusedRunID != "" {
				used[step.ReusedRunID] = true
				if step.Outcome == "succeeded" {
					stepsReused[step.Label] = true
				}
			}
		}
	}
	usage := UsageBreakdown{ReusedSteps: len(stepsReused)}
	var ids []string
	add := func(step, id string, attempt int, status api.ExecutionStatus, artifacts []api.ExecutionPipelineArtifactRef) {
		if id == "" || used[id] {
			return
		}
		used[id] = true
		ids = append(ids, id)
		usage.NewRuns++
		if attempt > 1 {
			usage.RetryRuns++
		}
		for _, artifact := range artifacts {
			usage.OutputBytes += int64(artifact.SizeBytes)
		}
		usage.OutputCount += len(artifacts)
		usage.Attempts = append(usage.Attempts, AttemptUsage{RunID: id, Step: step, Attempt: attempt,
			Status: status, outputs: len(artifacts)})
	}
	if receipt.Completion != nil {
		for _, step := range receipt.Completion.Steps {
			for _, a := range step.Attempts {
				add(step.Label, a.RunID, a.Attempt, a.Status, a.Artifacts)
			}
		}
	} else {
		for _, step := range receipt.Steps {
			for _, a := range step.Attempts {
				add(step.Label, a.RunID, a.Attempt, a.Status, nil)
			}
		}
	}
	return usage, ids
}

func settleAttempt(usage *UsageBreakdown, attempt *AttemptUsage, row state.RunUsageRecord) {
	usage.SettledRuns++
	usage.WallTimeMS += row.WallTimeMS
	attempt.LedgerState = "Recorded"
	attempt.Execution = time.Duration(row.WallTimeMS) * time.Millisecond
	if row.StartedAt != nil {
		queue := row.StartedAt.Sub(row.CreatedAt).Round(time.Millisecond)
		attempt.QueueTime = &queue
	}
	if attempt.outputs > 0 {
		attempt.OutputAt = &row.FinishedAt
	}
}
