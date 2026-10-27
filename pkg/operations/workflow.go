package operations

import (
	"fmt"
	"regexp"

	"github.com/onebox-faas/faas/pkg/api"
)

// ValidateWorkflow bounds the first execution adapter without weakening the
// native workflow contract. Progress has one ordered stage per step. Besides
// HTTP actions, the chain may park on a timer or a declared approval
// (ADR-1194), or on an event that a signal delivers (ADR-1197); the last step
// is an HTTP action that produces the result.
func ValidateWorkflow(operation api.OperationDefinitionSpec, workflow api.WorkflowSpec, plan api.Plan) error {
	if operation.Workflow != workflow.Name {
		return fmt.Errorf("operation workflow does not match the captured definition")
	}
	if _, err := api.ValidateWorkflowDAG(workflow, plan); err != nil {
		return err
	}
	if workflow.Code != nil {
		return fmt.Errorf("operation workflow cannot be a code-first workflow")
	}
	if workflow.Trigger != nil && workflow.Trigger.Type != "manual" {
		return fmt.Errorf("operation workflow requires a manual trigger")
	}
	if len(workflow.Steps) != len(operation.ProgressStages) {
		return fmt.Errorf("operation progress stages must match workflow steps in order")
	}
	approvals := map[string]bool{}
	for _, approval := range operation.Approvals {
		approvals[approval.Step] = true
	}
	for index, step := range workflow.Steps {
		if step.Name != operation.ProgressStages[index] || step.Outbound != nil || step.ForEach != nil || step.Join != nil || step.When != nil || step.OnFailure != "" || step.OnTimeout != "" || step.WaitForCondition != nil || step.Retry != nil && step.Retry.MaxAttempts != 1 {
			return fmt.Errorf("operation workflow supports ordered HTTP steps, timers, approvals and event waits without automatic retries or control actions")
		}
		switch WorkflowStepKind(step) {
		case api.OperationWaitTimer:
			if approvals[step.Name] {
				return fmt.Errorf("operation approval %q must be a callback step", step.Name)
			}
		case api.OperationWaitApproval:
			if !approvals[step.Name] {
				return fmt.Errorf("operation callback step %q must be a declared approval", step.Name)
			}
		case api.OperationWaitEvent:
			// ADR-1197: a signal is the only way to resume it, so the wait must end.
			if approvals[step.Name] || step.Timeout <= 0 || !ValidOperationSignalEvent(step.WaitForEvent) {
				return fmt.Errorf("operation event wait %q needs a timeout and a bounded event name", step.Name)
			}
		case "http":
			if approvals[step.Name] {
				return fmt.Errorf("operation approval %q must be a callback step", step.Name)
			}
		default:
			return fmt.Errorf("operation workflow step %q must be one HTTP action, timer, approval or event wait", step.Name)
		}
		if index == 0 && len(step.DependsOn) != 0 || index > 0 && (len(step.DependsOn) != 1 || step.DependsOn[0] != workflow.Steps[index-1].Name) {
			return fmt.Errorf("operation workflow steps must form one linear dependency chain")
		}
	}
	if WorkflowStepKind(workflow.Steps[len(workflow.Steps)-1]) != "http" {
		return fmt.Errorf("operation workflow must end with an HTTP action that produces the result")
	}
	return nil
}

// WorkflowStepKind classifies an operation workflow step: "http", a timer, an
// approval (callback) or "" for a mixed or unsupported shape.
func WorkflowStepKind(step api.WorkflowStepSpec) string {
	action := step.Run != "" || step.Path != ""
	switch {
	case action && step.WaitForDuration == 0 && !step.WaitForCallback:
		return "http"
	case !action && step.WaitForDuration > 0 && !step.WaitForCallback && step.Timeout == 0:
		return api.OperationWaitTimer
	case !action && step.WaitForCallback && step.WaitForDuration == 0:
		return api.OperationWaitApproval
	case !action && step.WaitForEvent != "" && !step.WaitForCallback && step.WaitForDuration == 0:
		return api.OperationWaitEvent
	}
	return ""
}

// compileApprovals validates the definition-local part of approvals; the
// workflow snapshot is checked by ValidateWorkflow.
func compileApprovals(spec *api.OperationDefinitionSpec) error {
	if len(spec.Approvals) == 0 {
		spec.Approvals = nil
		return nil
	}
	if spec.Workflow == "" {
		return fmt.Errorf("operation approvals require a workflow")
	}
	stages := map[string]bool{}
	for _, stage := range spec.ProgressStages {
		stages[stage] = true
	}
	seen := map[string]bool{}
	for _, approval := range spec.Approvals {
		if !stages[approval.Step] || seen[approval.Step] || approval.Approver != api.OperationApproverAccount && approval.Approver != api.OperationApproverCustomer {
			return fmt.Errorf("operation approvals must name unique workflow steps and an account or customer approver")
		}
		seen[approval.Step] = true
	}
	spec.Approvals = append([]api.OperationApprovalSpec(nil), spec.Approvals...)
	return nil
}

// ApprovalFor returns the declared approval for a step.
func ApprovalFor(spec api.OperationDefinitionSpec, step string) (api.OperationApprovalSpec, bool) {
	for _, approval := range spec.Approvals {
		if approval.Step == step {
			return approval, true
		}
	}
	return api.OperationApprovalSpec{}, false
}

var operationSignalEvent = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

// ValidOperationSignalEvent bounds event-wait and signal names. Callback
// names stay reserved for approvals.
func ValidOperationSignalEvent(name string) bool {
	return len(name) <= api.OperationSignalEventMaxBytes && operationSignalEvent.MatchString(name) && !api.IsWorkflowCallbackEventName(name)
}

// OperationSignalEvents lists the events a workflow snapshot waits for.
func OperationSignalEvents(workflow api.WorkflowSpec) map[string]bool {
	events := map[string]bool{}
	for _, step := range workflow.Steps {
		if WorkflowStepKind(step) == api.OperationWaitEvent {
			events[step.WaitForEvent] = true
		}
	}
	return events
}
