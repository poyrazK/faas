package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgWorkflowAutomationHealthAggregatesRunsAndSteps(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "automation-health-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "automation-health-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	windowStart, windowEnd := now.Add(-time.Hour), now.Add(time.Minute)

	createRun := func(status string, stepStatus string) *state.WorkflowRun {
		t.Helper()
		run := &state.WorkflowRun{
			AppID: app.ID, WorkflowName: "paid",
			Input:              json.RawMessage(`{"private":"input"}`),
			DefinitionSnapshot: json.RawMessage(`{"name":"paid","steps":[]}`),
		}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if status != state.WorkflowRunStatusPending {
			if err := store.MarkWorkflowRunStatus(ctx, run.ID, state.WorkflowRunStatusRunning, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		if stepStatus != "" {
			parent := "batch"
			index := 0
			if err := store.CreateWorkflowSteps(ctx, run.ID, []*state.WorkflowStep{{StepName: "batch[0]", Status: stepStatus, ForEachParent: &parent, ForEachIndex: &index}}); err != nil {
				t.Fatal(err)
			}
		}
		if status == state.WorkflowRunStatusSucceeded || status == state.WorkflowRunStatusFailed || status == state.WorkflowRunStatusDead {
			privateError := "private executor error"
			if err := store.MarkWorkflowRunStatus(ctx, run.ID, status, json.RawMessage(`{"private":"output"}`), &privateError); err != nil {
				t.Fatal(err)
			}
		}
		return run
	}

	createRun(state.WorkflowRunStatusRunning, "")
	success := createRun(state.WorkflowRunStatusSucceeded, "")
	failure := createRun(state.WorkflowRunStatusFailed, state.WorkflowStepStatusFailed)
	createRun(state.WorkflowRunStatusPending, "")

	health, err := store.GetWorkflowAutomationHealth(ctx, app.ID, "paid", windowStart, windowEnd)
	if err != nil {
		t.Fatal(err)
	}
	if health.RunCount != 4 || health.CompletedRunCount != 2 || health.StatusCounts[state.WorkflowRunStatusSucceeded] != 1 ||
		health.StatusCounts[state.WorkflowRunStatusFailed] != 1 || health.StatusCounts[state.WorkflowRunStatusRunning] != 1 || health.StatusCounts[state.WorkflowRunStatusPending] != 1 ||
		health.ActiveRunCount != 1 || health.QueuedRunCount != 1 {
		t.Fatalf("run totals/statuses = %+v", health)
	}
	if health.LastRun == nil || health.LastRun.Status != state.WorkflowRunStatusPending || health.LastSuccess == nil || health.LastSuccess.ID != success.ID || health.LastFailure == nil || health.LastFailure.ID != failure.ID {
		t.Fatalf("latest runs = %+v / %+v / %+v", health.LastRun, health.LastSuccess, health.LastFailure)
	}
	if health.P50DurationMS == nil || health.P95DurationMS == nil || *health.P50DurationMS < 0 || *health.P95DurationMS < *health.P50DurationMS {
		t.Fatalf("duration percentiles = %v/%v", health.P50DurationMS, health.P95DurationMS)
	}
	if len(health.FailedSteps) != 1 || health.FailedSteps[0].StepName != "batch" || health.FailedSteps[0].FailedRunCount != 1 || health.FailedSteps[0].LastFailedAt.IsZero() {
		t.Fatalf("failed steps = %+v", health.FailedSteps)
	}
}
