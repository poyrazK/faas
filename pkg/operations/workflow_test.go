package operations

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowOperationContractRejectsReplayAndControlActions(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(*api.WorkflowSpec)
	}{
		{"valid", func(*api.WorkflowSpec) {}},
		{"automatic retries", func(w *api.WorkflowSpec) { w.Steps[0].Retry = &api.WorkflowRetrySpec{MaxAttempts: 2, Backoff: "fixed"} }},
		{"parallel steps", func(w *api.WorkflowSpec) { w.Steps[1].DependsOn = nil }},
		{"progress mismatch", func(w *api.WorkflowSpec) { w.Steps[1].Name = "other" }},
		{"timer", func(w *api.WorkflowSpec) { w.Steps[1].Path = ""; w.Steps[1].WaitForDuration = time.Second }},
		{"compensation", func(w *api.WorkflowSpec) { w.Steps[0].OnFailure = w.Steps[1].Name }},
		{"trigger", func(w *api.WorkflowSpec) {
			w.Trigger = &api.WorkflowTriggerSpec{Type: "schedule", Schedule: "* * * * *", Timezone: "UTC"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := testSpec()
			op.Workflow = "export-chain"
			workflow := api.WorkflowSpec{Name: op.Workflow, Steps: []api.WorkflowStepSpec{{Name: "collecting", Path: "/collect"}, {Name: "uploading", Path: "/upload", DependsOn: []string{"collecting"}}}}
			test.alter(&workflow)
			err := ValidateWorkflow(op, workflow, api.PlanPro)
			if (err == nil) != (test.name == "valid") {
				t.Fatalf("contract validation=%v", err)
			}
		})
	}
	op := testSpec()
	op.Workflow = "export-chain"
	op.Recovery = api.OperationRecoverySafeRetry
	if _, err := Compile(op, api.MustLimitsFor(api.PlanPro).Operations); err == nil {
		t.Fatal("workflow accepted blind safe retry policy")
	}
}
