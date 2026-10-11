package operations

import (
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateWorkflow bounds the first execution adapter without weakening the
// native workflow contract. Progress has one ordered stage per HTTP action.
func ValidateWorkflow(operation api.OperationDefinitionSpec, workflow api.WorkflowSpec, plan api.Plan) error {
	if operation.Workflow != workflow.Name {
		return fmt.Errorf("operation workflow does not match the captured definition")
	}
	if _, err := api.ValidateWorkflowDAG(workflow, plan); err != nil {
		return err
	}
	if workflow.Trigger != nil && workflow.Trigger.Type != "manual" {
		return fmt.Errorf("operation workflow requires a manual trigger")
	}
	if len(workflow.Steps) != len(operation.ProgressStages) {
		return fmt.Errorf("operation progress stages must match workflow steps in order")
	}
	for index, step := range workflow.Steps {
		if step.Name != operation.ProgressStages[index] || (step.Run == "" && step.Path == "") || step.Outbound != nil || step.ForEach != nil || step.Join != nil || step.When != nil || step.OnFailure != "" || step.OnTimeout != "" || step.WaitForCondition != nil || step.WaitForCallback || step.WaitForEvent != "" || step.WaitForDuration != 0 || step.Retry != nil && step.Retry.MaxAttempts != 1 {
			return fmt.Errorf("operation workflow supports ordered HTTP steps without automatic retries or control actions")
		}
		if index == 0 && len(step.DependsOn) != 0 || index > 0 && (len(step.DependsOn) != 1 || step.DependsOn[0] != workflow.Steps[index-1].Name) {
			return fmt.Errorf("operation workflow steps must form one linear dependency chain")
		}
	}
	return nil
}
