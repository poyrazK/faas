package state

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type WorkflowAutomationRunSummary struct {
	ID         string
	Status     string
	CreatedAt  time.Time
	FinishedAt *time.Time
}

type WorkflowAutomationStepFailure struct {
	StepName       string
	FailedRunCount int64
	LastFailedAt   time.Time
}

type WorkflowAutomationHealth struct {
	RunCount          int64
	CompletedRunCount int64
	ActiveRunCount    int64
	QueuedRunCount    int64
	Queue             *api.AutomationQueueHealth
	StatusCounts      map[string]int64
	P50DurationMS     *int64
	P95DurationMS     *int64
	LastRun           *WorkflowAutomationRunSummary
	LastSuccess       *WorkflowAutomationRunSummary
	LastFailure       *WorkflowAutomationRunSummary
	FailedSteps       []WorkflowAutomationStepFailure
}

type WorkflowAutomationHealthStore interface {
	GetWorkflowAutomationHealth(context.Context, string, string, time.Time, time.Time) (WorkflowAutomationHealth, error)
}

func emptyWorkflowAutomationHealth() WorkflowAutomationHealth {
	return WorkflowAutomationHealth{
		StatusCounts: map[string]int64{
			WorkflowRunStatusPending: 0, WorkflowRunStatusRunning: 0,
			WorkflowRunStatusAwaitingEvent: 0, WorkflowRunStatusSucceeded: 0,
			WorkflowRunStatusFailed: 0, WorkflowRunStatusDead: 0,
		},
		FailedSteps: []WorkflowAutomationStepFailure{},
	}
}

func (m *MemStore) GetWorkflowAutomationHealth(ctx context.Context, appID, name string, after, before time.Time) (WorkflowAutomationHealth, error) {
	if appID == "" || name == "" || after.After(before) || before.Sub(after) > api.WorkflowAutomationHealthMaxRange {
		return WorkflowAutomationHealth{}, ErrWorkflowInvalidCreatedRange
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return WorkflowAutomationHealth{}, err
	}

	health := emptyWorkflowAutomationHealth()
	health.Queue = m.workflowAutomationQueueHealthLocked(appID, name, time.Now().UTC())
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
		summary := workflowAutomationRunSummary(run)
		if newerWorkflowAutomationRun(summary, health.LastRun) {
			health.LastRun = summary
		}
		if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
			health.CompletedRunCount++
			if run.StartedAt != nil && run.FinishedAt != nil && !run.FinishedAt.Before(*run.StartedAt) {
				durations = append(durations, float64(run.FinishedAt.Sub(*run.StartedAt))/float64(time.Millisecond))
			}
		}
		if run.Status == WorkflowRunStatusSucceeded && newerWorkflowAutomationRun(summary, health.LastSuccess) {
			health.LastSuccess = summary
		}
		if run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
			if newerWorkflowAutomationRun(summary, health.LastFailure) {
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
		health.P50DurationMS = workflowAutomationPercentile(durations, .50)
		health.P95DurationMS = workflowAutomationPercentile(durations, .95)
	}
	health.FailedSteps = workflowAutomationFailedSteps(topFailures)
	return health, nil
}

func workflowAutomationRunSummary(run WorkflowRun) *WorkflowAutomationRunSummary {
	return &WorkflowAutomationRunSummary{ID: run.ID, Status: run.Status, CreatedAt: run.CreatedAt, FinishedAt: cloneWorkflowTime(run.FinishedAt)}
}

func newerWorkflowAutomationRun(candidate, current *WorkflowAutomationRunSummary) bool {
	return current == nil || candidate.CreatedAt.After(current.CreatedAt) ||
		(candidate.CreatedAt.Equal(current.CreatedAt) && candidate.ID > current.ID)
}

func workflowAutomationPercentile(values []float64, percentile float64) *int64 {
	sort.Float64s(values)
	position := percentile * float64(len(values)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	value := values[lower] + (values[upper]-values[lower])*(position-float64(lower))
	result := int64(math.Round(value))
	return &result
}

func workflowAutomationFailedSteps(byStep map[string]map[string]time.Time) []WorkflowAutomationStepFailure {
	steps := make([]WorkflowAutomationStepFailure, 0, len(byStep))
	for name, runs := range byStep {
		failure := WorkflowAutomationStepFailure{StepName: name, FailedRunCount: int64(len(runs))}
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
	if len(steps) > api.WorkflowAutomationHealthMaxFailureSteps {
		steps = steps[:api.WorkflowAutomationHealthMaxFailureSteps]
	}
	return steps
}
