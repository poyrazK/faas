package state

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type StoredWorkflowRunSummary struct {
	ID         string
	Status     string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

type StoredWorkflowStepFailure struct {
	StepName       string
	FailedRunCount int64
	LastFailedAt   time.Time
}

type StoredWorkflowHealth struct {
	RunCount          int64
	CompletedRunCount int64
	ActiveRunCount    int64
	QueuedRunCount    int64
	Queue             *api.StoredWorkflowQueueHealth
	StatusCounts      map[string]int64
	P50DurationMS     *int64
	P95DurationMS     *int64
	LastRun           *StoredWorkflowRunSummary
	LastSuccess       *StoredWorkflowRunSummary
	LastFailure       *StoredWorkflowRunSummary
	FailedSteps       []StoredWorkflowStepFailure
}

type StoredWorkflowHealthStore interface {
	GetStoredWorkflowHealth(context.Context, string, string, time.Time, time.Time) (StoredWorkflowHealth, error)
}

func emptyStoredWorkflowHealth() StoredWorkflowHealth {
	return StoredWorkflowHealth{
		StatusCounts: map[string]int64{
			WorkflowRunStatusPending: 0, WorkflowRunStatusRunning: 0,
			WorkflowRunStatusAwaitingEvent: 0, WorkflowRunStatusSucceeded: 0,
			WorkflowRunStatusFailed: 0, WorkflowRunStatusDead: 0,
		},
		FailedSteps: []StoredWorkflowStepFailure{},
	}
}

func (m *MemStore) GetStoredWorkflowHealth(ctx context.Context, appID, name string, after, before time.Time) (StoredWorkflowHealth, error) {
	if appID == "" || name == "" || after.After(before) || before.Sub(after) > api.StoredWorkflowHealthMaxRange {
		return StoredWorkflowHealth{}, ErrWorkflowInvalidCreatedRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StoredWorkflowHealth{}, err
	}

	health := emptyStoredWorkflowHealth()
	health.Queue = m.storedWorkflowQueueHealthLocked(appID, name, time.Now().UTC())
	durations := make([]float64, 0)
	topFailures := make(map[string]map[string]time.Time)
	for _, run := range m.workflowRuns {
		if run.AppID != appID || run.WorkflowName != name {
			continue
		}
		if workflowRunConsumesConcurrency(run) {
			health.ActiveRunCount++
		} else if run.Status == WorkflowRunStatusPending {
			health.QueuedRunCount++
		}
		if run.CreatedAt.Before(after) || run.CreatedAt.After(before) {
			continue
		}
		health.RunCount++
		health.StatusCounts[run.Status]++
		summary := storedWorkflowRunSummary(run)
		if newerStoredWorkflowRun(summary, health.LastRun) {
			health.LastRun = summary
		}
		if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
			health.CompletedRunCount++
			if run.StartedAt != nil && run.FinishedAt != nil && !run.FinishedAt.Before(*run.StartedAt) {
				durations = append(durations, float64(run.FinishedAt.Sub(*run.StartedAt))/float64(time.Millisecond))
			}
		}
		if run.Status == WorkflowRunStatusSucceeded && newerStoredWorkflowRun(summary, health.LastSuccess) {
			health.LastSuccess = summary
		}
		if run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
			if newerStoredWorkflowRun(summary, health.LastFailure) {
				health.LastFailure = summary
			}
			failedAt := run.CreatedAt
			if run.FinishedAt != nil {
				failedAt = *run.FinishedAt
			}
			perRun := make(map[string]struct{})
			for _, step := range m.workflowSteps[run.ID] {
				if step.Status != WorkflowStepStatusFailed && step.Status != WorkflowStepStatusDead {
					continue
				}
				stepName := step.StepName
				if step.ForEachParent != nil {
					stepName = *step.ForEachParent
				}
				perRun[stepName] = struct{}{}
			}
			for stepName := range perRun {
				if topFailures[stepName] == nil {
					topFailures[stepName] = make(map[string]time.Time)
				}
				topFailures[stepName][run.ID] = failedAt
			}
		}
	}
	if len(durations) > 0 {
		health.P50DurationMS = storedWorkflowPercentile(durations, .50)
		health.P95DurationMS = storedWorkflowPercentile(durations, .95)
	}
	health.FailedSteps = storedWorkflowFailedSteps(topFailures)
	return health, nil
}

func storedWorkflowRunSummary(run WorkflowRun) *StoredWorkflowRunSummary {
	return &StoredWorkflowRunSummary{ID: run.ID, Status: run.Status, CreatedAt: run.CreatedAt, FinishedAt: cloneWorkflowTime(run.FinishedAt)}
}

func newerStoredWorkflowRun(candidate, current *StoredWorkflowRunSummary) bool {
	return current == nil || candidate.CreatedAt.After(current.CreatedAt) ||
		(candidate.CreatedAt.Equal(current.CreatedAt) && candidate.ID > current.ID)
}

func storedWorkflowPercentile(values []float64, percentile float64) *int64 {
	sort.Float64s(values)
	position := percentile * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	value := values[lower] + (values[upper]-values[lower])*(position-float64(lower))
	result := int64(math.Round(value))
	return &result
}

func storedWorkflowFailedSteps(byStep map[string]map[string]time.Time) []StoredWorkflowStepFailure {
	steps := make([]StoredWorkflowStepFailure, 0, len(byStep))
	for name, runs := range byStep {
		failure := StoredWorkflowStepFailure{StepName: name, FailedRunCount: int64(len(runs))}
		for _, at := range runs {
			if at.After(failure.LastFailedAt) {
				failure.LastFailedAt = at
			}
		}
		steps = append(steps, failure)
	}
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].FailedRunCount == steps[j].FailedRunCount {
			return steps[i].StepName < steps[j].StepName
		}
		return steps[i].FailedRunCount > steps[j].FailedRunCount
	})
	if len(steps) > api.StoredWorkflowHealthMaxFailureSteps {
		steps = steps[:api.StoredWorkflowHealthMaxFailureSteps]
	}
	return steps
}
