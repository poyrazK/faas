package faas

import (
	"errors"
	"github.com/poyrazK/faas/sdk/go/internal/api"
)

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

// WorkflowPathDecision links a chosen path to a decision already queued in this transaction.
func (tx *CustomerOperationTransaction) WorkflowPathDecision(group, path, milestone string) (OperationWorkflowPathSelection, error) {
	if tx == nil || !tx.open {
		return OperationWorkflowPathSelection{}, errors.New("faas: path decisions require an open transaction")
	}
	var selection OperationWorkflowPathSelection
	count := 0
	for _, fact := range tx.milestones {
		if fact.Name != milestone {
			continue
		}
		decision, err := api.ParseOperationBusinessDecision(fact.Payload)
		if err != nil || decision == nil {
			return selection, errors.New("faas: path reference requires a business decision milestone")
		}
		count++
		selection = OperationWorkflowPathSelection{GroupID: group, PathID: path, DecisionMilestoneID: fact.ID, DecisionCode: decision.Code, DecisionDescription: decision.Description, DecisionRuleID: decision.RuleID, DecisionRuleVersion: decision.RuleVersion}
	}
	if count != 1 {
		return selection, errors.New("faas: path reference requires exactly one queued decision milestone")
	}
	canonical, err := canonicalCustomerWorkflowPaths([]OperationWorkflowPathSelection{selection})
	if err != nil {
		return selection, err
	}
	return canonical[0], nil
}
