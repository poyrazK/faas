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

// ADR-1194: timers and declared approvals join the linear chain; every other
// wait or control action stays excluded and the result comes from an action.
func TestWorkflowOperationContractAdmitsTimersAndApprovals(t *testing.T) {
	for _, test := range []struct {
		name  string
		alter func(*api.OperationDefinitionSpec, *api.WorkflowSpec)
		valid bool
	}{
		{"valid", func(*api.OperationDefinitionSpec, *api.WorkflowSpec) {}, true},
		{"undeclared callback", func(op *api.OperationDefinitionSpec, _ *api.WorkflowSpec) { op.Approvals = nil }, false},
		{"approval on an action", func(op *api.OperationDefinitionSpec, _ *api.WorkflowSpec) {
			op.Approvals = append(op.Approvals, api.OperationApprovalSpec{Step: "collecting", Approver: api.OperationApproverAccount})
		}, false},
		{"approval on a timer", func(op *api.OperationDefinitionSpec, _ *api.WorkflowSpec) {
			op.Approvals = append(op.Approvals, api.OperationApprovalSpec{Step: "cool", Approver: api.OperationApproverAccount})
		}, false},
		{"ends with a wait", func(op *api.OperationDefinitionSpec, w *api.WorkflowSpec) {
			w.Steps = w.Steps[:3]
			op.ProgressStages = op.ProgressStages[:3]
		}, false},
		{"event wait", func(_ *api.OperationDefinitionSpec, w *api.WorkflowSpec) {
			w.Steps[2] = api.WorkflowStepSpec{Name: "cool", WaitForEvent: "invoice.paid", Timeout: time.Hour, DependsOn: []string{"review"}}
		}, false},
		{"timeout handler", func(_ *api.OperationDefinitionSpec, w *api.WorkflowSpec) { w.Steps[1].OnTimeout = "uploading" }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			op := testSpec()
			op.Workflow = "refund-chain"
			op.ProgressStages = []string{"collecting", "review", "cool", "uploading"}
			op.Approvals = []api.OperationApprovalSpec{{Step: "review", Approver: api.OperationApproverCustomer}}
			workflow := api.WorkflowSpec{Name: op.Workflow, Steps: []api.WorkflowStepSpec{
				{Name: "collecting", Path: "/collect"},
				{Name: "review", WaitForCallback: true, Timeout: time.Hour, DependsOn: []string{"collecting"}},
				{Name: "cool", WaitForDuration: time.Second, DependsOn: []string{"review"}},
				{Name: "uploading", Path: "/upload", DependsOn: []string{"cool"}},
			}}
			test.alter(&op, &workflow)
			if err := ValidateWorkflow(op, workflow, api.PlanPro); (err == nil) != test.valid {
				t.Fatalf("contract validation=%v", err)
			}
		})
	}
}

func TestOperationApprovalsCompile(t *testing.T) {
	limits := api.MustLimitsFor(api.PlanPro).Operations
	base := testSpec()
	base.Workflow = "refund-chain"
	base.ProgressStages = []string{"collecting", "review", "uploading"}
	plain, err := Compile(base, limits)
	if err != nil {
		t.Fatal(err)
	}
	withApproval := base
	withApproval.Approvals = []api.OperationApprovalSpec{{Step: "review", Approver: api.OperationApproverAccount}}
	contract, err := Compile(withApproval, limits)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Revision == plain.Revision || len(contract.Spec.Approvals) != 1 {
		t.Fatal("approvals must be part of the immutable revision")
	}
	for name, approvals := range map[string][]api.OperationApprovalSpec{
		"unknown approver": {{Step: "review", Approver: "manager"}},
		"unknown step":     {{Step: "missing", Approver: api.OperationApproverAccount}},
		"duplicate step":   {{Step: "review", Approver: api.OperationApproverAccount}, {Step: "review", Approver: api.OperationApproverCustomer}},
	} {
		invalid := base
		invalid.Approvals = approvals
		if _, err := Compile(invalid, limits); err == nil {
			t.Errorf("%s compiled", name)
		}
	}
	http := testSpec()
	http.Approvals = []api.OperationApprovalSpec{{Step: http.ProgressStages[0], Approver: api.OperationApproverAccount}}
	if _, err := Compile(http, limits); err == nil {
		t.Fatal("an HTTP operation declared an approval")
	}
}
