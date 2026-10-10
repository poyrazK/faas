package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type OperationBusinessDecision = api.OperationBusinessDecision
type OperationBusinessDecisionPayload = api.OperationBusinessDecisionPayload

// BusinessDecision queues reasoning as a declared milestone with the business write.
// Bind its workflow step instance_id_from to /decision/instance_id in the contract.
func (tx *CustomerOperationTransaction) BusinessDecision(milestone string, decision OperationBusinessDecision) error {
	if err := api.ValidateOperationBusinessDecision(decision); err != nil {
		return err
	}
	return tx.Milestone(milestone, OperationBusinessDecisionPayload{Kind: api.OperationBusinessDecisionKind, Decision: decision})
}

type OperationWorkflowPolicyRequirement = api.OperationWorkflowPolicyRequirement
type OperationWorkflowPlannedDecision = api.OperationWorkflowPlannedDecision
