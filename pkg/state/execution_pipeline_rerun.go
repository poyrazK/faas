package state

import (
	"github.com/onebox-faas/faas/pkg/api"
	"reflect"
)

func validatePipelineRerun(parent ExecutionWorkflowJob, meta ExecutionPipeline, p CreateExecutionWorkflowJobParams, policy ExecutionPipelinePolicy) error {
	r := policy.Rerun
	if r == nil {
		return nil
	}
	if parent.ID != r.ParentID || parent.AccountID != p.AccountID || !sameOptionalString(parent.RunsPrincipalID, p.RunsPrincipalID) || parent.Status != api.ManagedExecutionWorkflowFailed || meta.Completion == nil || meta.Policy.Callback != nil || parent.WorkflowID != r.ParentWorkflowID || parent.PlanID != r.ParentPlanID || parent.WorkflowID == p.WorkflowID || len(policy.Steps) != len(meta.Policy.Steps) {
		return ErrExecutionInvalid
	}
	seen := map[string]bool{}
	for _, ref := range r.Reused {
		if seen[ref.Label] || !validEnvironmentID(ref.RunID) || len(ref.OutputSHA256) != 64 {
			return ErrExecutionInvalid
		}
		seen[ref.Label] = true
		found := false
		for i, step := range meta.Completion.Steps {
			if step.Label != ref.Label {
				continue
			}
			if step.Outcome != "succeeded" || policy.Steps[i].Label != step.Label || !reflect.DeepEqual(policy.Steps[i].Task, meta.Policy.Steps[i].Task) {
				return ErrExecutionInvalid
			}
			id := step.ReusedRunID
			if len(step.Attempts) > 0 {
				id = step.Attempts[len(step.Attempts)-1].RunID
			}
			found = id == ref.RunID && ref.OutputSHA256 == step.OutputSHA256
		}
		if !found {
			return ErrExecutionInvalid
		}
	}
	return nil
}
