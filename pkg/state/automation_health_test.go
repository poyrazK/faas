package state

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemStoreWorkflowAutomationHealth(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	appID, now := uuid.NewString(), time.Now().UTC().Truncate(time.Millisecond)
	after, before := now.Add(-time.Hour), now

	newRun := func(name, status string, created time.Time, duration time.Duration) *WorkflowRun {
		t.Helper()
		run := &WorkflowRun{
			AppID: appID, WorkflowName: name, Input: json.RawMessage(`{"private_input":"secret"}`),
			DefinitionSnapshot: json.RawMessage(`{"name":"invoice","steps":[]}`),
		}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		started, finished := created.Add(-duration), created
		store.mu.Lock()
		stored := store.workflowRuns[run.ID]
		stored.Status = status
		stored.CreatedAt = created
		if status != WorkflowRunStatusPending {
			stored.StartedAt = &started
		}
		if status == WorkflowRunStatusSucceeded || status == WorkflowRunStatusFailed || status == WorkflowRunStatusDead {
			stored.FinishedAt = &finished
		}
		store.workflowRuns[run.ID] = stored
		store.mu.Unlock()
		return run
	}

	newRun("invoice", WorkflowRunStatusSucceeded, now.Add(-30*time.Minute), 100*time.Millisecond)
	failedOne := newRun("invoice", WorkflowRunStatusFailed, now.Add(-20*time.Minute), 300*time.Millisecond)
	failedTwo := newRun("invoice", WorkflowRunStatusDead, now.Add(-10*time.Minute), 500*time.Millisecond)
	newRun("invoice", WorkflowRunStatusRunning, now.Add(-15*time.Minute), 0)
	newRun("other", WorkflowRunStatusFailed, now.Add(-5*time.Minute), time.Second)
	newRun("invoice", WorkflowRunStatusPending, now.Add(-2*time.Hour), 0)

	parent := "batch"
	if err := store.CreateWorkflowSteps(ctx, failedOne.ID, []*WorkflowStep{
		{StepName: "batch[0]", Status: WorkflowStepStatusFailed, ForEachParent: &parent},
		{StepName: "batch[1]", Status: WorkflowStepStatusDead, ForEachParent: &parent},
		{StepName: "notify", Status: WorkflowStepStatusFailed},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateWorkflowSteps(ctx, failedTwo.ID, []*WorkflowStep{{StepName: "batch[2]", Status: WorkflowStepStatusFailed, ForEachParent: &parent}}); err != nil {
		t.Fatal(err)
	}

	health, err := store.GetWorkflowAutomationHealth(ctx, appID, "invoice", after, before)
	if err != nil {
		t.Fatal(err)
	}
	if health.RunCount != 4 || health.CompletedRunCount != 3 || health.StatusCounts[WorkflowRunStatusSucceeded] != 1 ||
		health.StatusCounts[WorkflowRunStatusFailed] != 1 || health.StatusCounts[WorkflowRunStatusDead] != 1 || health.StatusCounts[WorkflowRunStatusRunning] != 1 || health.StatusCounts[WorkflowRunStatusPending] != 0 ||
		health.ActiveRunCount != 1 || health.QueuedRunCount != 1 {
		t.Fatalf("run totals/statuses = %+v", health)
	}
	if health.P50DurationMS == nil || *health.P50DurationMS != 300 || health.P95DurationMS == nil || *health.P95DurationMS != 480 {
		t.Fatalf("duration percentiles = %v/%v", health.P50DurationMS, health.P95DurationMS)
	}
	if health.LastRun == nil || health.LastRun.ID != failedTwo.ID || health.LastSuccess == nil || health.LastSuccess.Status != WorkflowRunStatusSucceeded || health.LastFailure == nil || health.LastFailure.ID != failedTwo.ID {
		t.Fatalf("latest runs = %+v / %+v / %+v", health.LastRun, health.LastSuccess, health.LastFailure)
	}
	if len(health.FailedSteps) != 2 || health.FailedSteps[0].StepName != "batch" || health.FailedSteps[0].FailedRunCount != 2 ||
		!health.FailedSteps[0].LastFailedAt.Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("failed-step aggregation = %+v", health.FailedSteps)
	}
	if health.FailedSteps[1].StepName != "notify" || health.FailedSteps[1].FailedRunCount != 1 {
		t.Fatalf("failed-step ranking = %+v", health.FailedSteps)
	}

	if _, err := store.GetWorkflowAutomationHealth(ctx, appID, "invoice", after, before.Add(api.WorkflowAutomationHealthMaxRange)); !errors.Is(err, ErrWorkflowInvalidCreatedRange) {
		t.Fatalf("overlong window error = %v", err)
	}
}
