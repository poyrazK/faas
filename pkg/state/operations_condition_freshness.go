package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// The source fact belongs to a milestone on the reporting Operation's immutable definition.
func workflowReportConditionDefinitions(steps []api.OperationWorkflowSpec, milestones map[string]operationMilestoneReceipt, history api.OperationWorkflowStateHistoryEntry) map[string][]api.OperationBusinessConditionDefinition {
	ids := map[string]bool{}
	if history.ChangeReason != nil {
		ids[history.ChangeReason.MilestoneID] = true
	}
	for _, path := range history.SelectedPaths {
		if path.DecisionMilestoneID != "" {
			ids[path.DecisionMilestoneID] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	out := map[string][]api.OperationBusinessConditionDefinition{}
	for id := range ids {
		receipt, found := milestones[id]
		if !found {
			continue
		}
		for _, step := range steps {
			if step.Workflow == history.Workflow && effectiveWorkflowContractVersion(step.Version) == effectiveWorkflowContractVersion(history.ContractVersion) && step.Milestone == receipt.Milestone.Name {
				out[id] = append([]api.OperationBusinessConditionDefinition{}, step.DecisionConditions...)
			}
		}
	}
	return out
}
func workflowConditionFreshnessDefinition(definitions []api.OperationBusinessConditionDefinition, result api.OperationBusinessConditionResult) *api.OperationWorkflowConditionFreshness {
	out := &api.OperationWorkflowConditionFreshness{Status: "no_budget"}
	for _, definition := range definitions {
		if definition.ID == result.ID && definition.Description == result.Description {
			out.EvidenceMaxAgeSeconds = definition.EvidenceMaxAgeSeconds
			break
		}
	}
	return out
}
func workflowEvaluateConditionFreshness(fact *api.OperationWorkflowDecisionOutcomeEvidence, at time.Time) {
	// Copy before deriving deadlines: aggregate evaluation must not mutate retained evidence.
	freshness := api.OperationWorkflowConditionFreshness{Status: "no_budget"}
	if fact.ConditionFreshness != nil {
		freshness.EvidenceMaxAgeSeconds = fact.ConditionFreshness.EvidenceMaxAgeSeconds
	}
	if freshness.EvidenceMaxAgeSeconds > 0 {
		due := fact.OccurredAt.Add(time.Duration(freshness.EvidenceMaxAgeSeconds) * time.Second)
		freshness.RefreshDueAt = &due
		freshness.Status = "within_budget"
		if !at.Before(due) {
			freshness.Status = "overdue"
			freshness.OverdueSeconds = int64(at.Sub(due) / time.Second)
		}
	}
	fact.ConditionFreshness = &freshness
}
func workflowActiveConditionStatusCount(group api.OperationWorkflowActiveConditionGroup, status string) int64 {
	switch status {
	case "within_budget":
		return group.WithinBudgetWorkflowCount
	case "overdue":
		return group.OverdueWorkflowCount
	case "no_budget":
		return group.NoBudgetWorkflowCount
	default:
		return group.ElapsedTime.WorkflowCount
	}
}
