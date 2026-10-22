// adr: 1196
package operations

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOperationLimitsCompile(t *testing.T) {
	plan := api.MustLimitsFor(api.PlanPro).Operations
	base := testSpec()
	plain, err := Compile(base, plan)
	if err != nil {
		t.Fatal(err)
	}
	bounded := base
	bounded.Limits = &api.OperationLimits{DeadlineSeconds: 3600, MaxAttempts: 5, MaxExecutionSeconds: 300}
	contract, err := Compile(bounded, plan)
	if err != nil {
		t.Fatal(err)
	}
	if contract.Revision == plain.Revision || *contract.Spec.Limits != *bounded.Limits {
		t.Fatal("limits must be part of the immutable revision")
	}
	if contract.Spec.Limits == bounded.Limits {
		t.Fatal("the compiled contract aliases the caller's limits")
	}
	workflow := base
	workflow.Workflow = "refund-chain"
	job := base
	job.Job = "export-job"
	for name, tc := range map[string]struct {
		spec   api.OperationDefinitionSpec
		limits api.OperationLimits
		want   string
	}{
		"empty block":        {base, api.OperationLimits{}, "at least one bound"},
		"negative deadline":  {base, api.OperationLimits{DeadlineSeconds: -1}, "deadline_seconds"},
		"deadline over plan": {base, api.OperationLimits{DeadlineSeconds: plan.DeadlineSecondsMax + 1}, "deadline_seconds"},
		"attempts over plan": {base, api.OperationLimits{MaxAttempts: plan.AttemptsMax + 1}, "max_attempts"},
		"budget over plan":   {base, api.OperationLimits{MaxExecutionSeconds: plan.ExecutionSecondsMax + 1}, "max_execution_seconds"},
		"workflow attempts":  {workflow, api.OperationLimits{MaxAttempts: 2}, "HTTP operations"},
		"job limits":         {job, api.OperationLimits{DeadlineSeconds: 60}, "job operations"},
	} {
		t.Run(name, func(t *testing.T) {
			spec := tc.spec
			limits := tc.limits
			spec.Limits = &limits
			if _, err := Compile(spec, plan); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Compile = %v, want error containing %q", err, tc.want)
			}
		})
	}
	// ADR-20261016110803: workflows take a deadline and an execution budget.
	workflow.Limits = &api.OperationLimits{DeadlineSeconds: 600, MaxExecutionSeconds: 60}
	workflow.ProgressStages = []string{"collecting"}
	if _, err := Compile(workflow, plan); err != nil && (strings.Contains(err.Error(), "limits") || strings.Contains(err.Error(), "max_")) {
		t.Fatalf("workflow deadline and budget rejected: %v", err)
	}
}
