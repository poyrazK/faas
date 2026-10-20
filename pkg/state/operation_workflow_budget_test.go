// adr: 20261016110803
package state

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowOperationUsageCountsFinishedHTTPAttempts(t *testing.T) {
	snapshot, _ := json.Marshal(api.WorkflowSpec{Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate"},
		{Name: "review", WaitForCallback: true, Timeout: time.Hour},
		{Name: "cool", WaitForDuration: time.Minute},
		{Name: "finish", Path: "/finish"},
	}})
	start := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	ended := func(d time.Duration) *time.Time { at := start.Add(d); return &at }
	usage, err := workflowOperationUsage(snapshot, []WorkflowStepAttempt{
		{StepName: "validate", Attempt: 1, StartedAt: start, FinishedAt: ended(300 * time.Millisecond)},
		{StepName: "validate", Attempt: 2, StartedAt: start, FinishedAt: ended(700 * time.Millisecond)},
		{StepName: "review", Attempt: 1, StartedAt: start, FinishedAt: ended(time.Hour)},
		{StepName: "cool", Attempt: 1, StartedAt: start, FinishedAt: ended(time.Minute)},
		{StepName: "finish", Attempt: 1, StartedAt: start}, // still running
	})
	if err != nil || usage != (api.OperationUsage{ExecutionMS: 1000, Attempts: 2}) {
		t.Fatalf("usage %+v %v", usage, err)
	}
	if _, err := workflowOperationUsage([]byte("{"), nil); err == nil {
		t.Fatal("a corrupt snapshot was read as no usage")
	}
}

func TestWorkflowOperationRemainingBudget(t *testing.T) {
	if workflowOperationRemaining(0, api.OperationUsage{ExecutionMS: 5}) != nil {
		t.Error("an Operation without a budget got a remaining budget")
	}
	if got := *workflowOperationRemaining(2, api.OperationUsage{ExecutionMS: 500}); got != 1500*time.Millisecond {
		t.Errorf("remaining %s", got)
	}
	if got := *workflowOperationRemaining(1, api.OperationUsage{ExecutionMS: 1200}); got >= 0 {
		t.Errorf("a spent budget left %s", got)
	}
}
