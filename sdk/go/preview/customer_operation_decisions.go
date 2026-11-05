package preview

import (
	"errors"

	"github.com/poyrazK/faas/sdk/go/internal/api"
)

type OperationBusinessConditionDefinition = api.OperationBusinessConditionDefinition
type OperationBusinessConditionResult = api.OperationBusinessConditionResult
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
		selection = OperationWorkflowPathSelection{GroupID: group, PathID: path, DecisionConditions: decision.Conditions, DecisionMilestoneID: fact.ID, DecisionCode: decision.Code, DecisionDescription: decision.Description, DecisionRuleID: decision.RuleID, DecisionRuleVersion: decision.RuleVersion}
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

type OperationWorkflowChangeReason = api.OperationWorkflowChangeReason

// WorkflowChangeReason attaches a queued decision to the most recent workflow update.
func (tx *CustomerOperationTransaction) WorkflowChangeReason(milestone string) error {
	selection, err := tx.WorkflowPathDecision("change", "reason", milestone)
	if err != nil {
		return err
	}
	if len(tx.workflowStates) == 0 {
		return errors.New("faas: queue a workflow update before its change reason")
	}
	index := len(tx.workflowStates) - 1
	prior := tx.workflowStates[index]
	next := prior
	if prior.ChangeReason != nil {
		return errors.New("faas: a change reason is already attached to this report")
	}
	if next.FromState == "" {
		return errors.New("faas: initial snapshots cannot carry a change reason")
	}
	for _, fact := range tx.milestones {
		if fact.ID != selection.DecisionMilestoneID {
			continue
		}
		d, parseErr := api.ParseOperationBusinessDecision(fact.Payload)
		if parseErr != nil || d == nil || d.Workflow != next.Workflow || d.InstanceID != next.InstanceID {
			return errors.New("faas: change reason requires a matching workflow decision")
		}
		next.ChangeReason = &OperationWorkflowChangeReason{MilestoneID: fact.ID, Decision: *d}
		next.EvidenceMilestones = append([]OperationWorkflowEvidenceMilestone(nil), prior.EvidenceMilestones...)
		present := false
		for _, e := range next.EvidenceMilestones {
			present = present || e.ID == fact.ID
		}
		if !present {
			next.EvidenceMilestones = append(next.EvidenceMilestones, OperationWorkflowEvidenceMilestone{ID: fact.ID, Name: fact.Name})
		}
	}
	tx.workflowStates[index] = next
	batch, batchErr := marshalCustomerOperationJSON(struct {
		WorkflowStates []OperationWorkflowStateReport `json:"workflow_states"`
	}{tx.workflowStates})
	if batchErr != nil || len(batch) > operationWorkflowStateBatchBytes {
		tx.workflowStates[index] = prior
		return errors.New("faas: workflow state batch exceeds its byte limit")
	}
	return nil
}
func sameCustomerWorkflowChangeReason(a, b *OperationWorkflowChangeReason) bool {
	return a == nil && b == nil || a != nil && b != nil && a.MilestoneID == b.MilestoneID && api.SameOperationBusinessDecision(a.Decision, b.Decision)
}
