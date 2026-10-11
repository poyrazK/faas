package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type OperationBusinessInvariant = api.OperationBusinessInvariant
type OperationBusinessInvariantPayload = api.OperationBusinessInvariantPayload

// ReportBusinessInvariant queues a declared check fact and a blocker-only update.
// Evaluate under the application's row locks and supply its complete blocker head.
// Returned blockers can be supplied to subsequent checks in this transaction.
func (tx *CustomerOperationTransaction) ReportBusinessInvariant(milestone string, input OperationBusinessInvariant, current []OperationWorkflowBlocker) (blockers []OperationWorkflowBlocker, err error) {
	if tx == nil || !tx.open {
		return nil, ErrInvalidCustomerOperationRequest
	}
	defer func() {
		if err != nil && tx.guardErr == nil {
			tx.guardErr = err
		}
	}()
	if tx.guardErr != nil {
		return nil, tx.guardErr
	}
	input, err = api.CanonicalOperationBusinessInvariant(input)
	if err != nil {
		return nil, err
	}
	blockers, err = api.ApplyOperationBusinessInvariant(input, current)
	if err != nil {
		return nil, err
	}
	staged := *tx
	staged.milestones = append([]OperationMilestoneRequest(nil), tx.milestones...)
	staged.workflowStates = append([]OperationWorkflowStateReport(nil), tx.workflowStates...)
	if err = staged.Milestone(milestone, OperationBusinessInvariantPayload{Kind: api.OperationBusinessInvariantKind, Invariant: input}); err != nil {
		return nil, err
	}
	if err = staged.WorkflowBlockers(input.Workflow, input.InstanceID, input.State, blockers); err != nil {
		return nil, err
	}
	tx.milestones, tx.workflowStates = staged.milestones, staged.workflowStates
	return blockers, nil
}

type OperationWorkflowInvariantRequirement = api.OperationWorkflowInvariantRequirement
type OperationWorkflowPlannedInvariant = api.OperationWorkflowPlannedInvariant
type OperationWorkflowUnmetInvariant = api.OperationWorkflowUnmetInvariant
