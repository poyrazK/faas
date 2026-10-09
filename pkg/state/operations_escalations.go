package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func workflowBlockerEscalations(spec api.OperationDefinitionSpec, state api.OperationWorkflowState, at time.Time) []api.OperationWorkflowBlockerEscalation {
	if state.Terminal {
		return nil
	}
	var policies map[string]api.OperationWorkflowBlockerEscalationPolicy
	for _, step := range spec.WorkflowSteps {
		if step.Workflow == state.Workflow && effectiveWorkflowContractVersion(step.Version) == state.ContractVersion {
			policies = step.BlockerEscalations
			break
		}
	}
	var result []api.OperationWorkflowBlockerEscalation
	for _, b := range state.Blockers {
		policy, ok := policies[b.Code]
		first, err := time.Parse(time.RFC3339Nano, b.FirstObservedAt)
		if !ok || err != nil || policy.AfterSeconds < 1 {
			continue
		}
		due := first.Add(time.Duration(policy.AfterSeconds) * time.Second).UTC()
		if !at.Before(due) {
			result = append(result, api.OperationWorkflowBlockerEscalation{Code: b.Code, Operation: b.Operation, Owner: policy.Owner, AfterSeconds: policy.AfterSeconds, EscalatedAt: due})
		}
	}
	return result
}
func workflowBlockerEscalated(b api.OperationWorkflowBlocker, escalations []api.OperationWorkflowBlockerEscalation) bool {
	for _, e := range escalations {
		if e.Code == b.Code && e.Operation == b.Operation {
			return true
		}
	}
	return false
}
func workflowEscalationMatches(blockers []api.OperationWorkflowBlocker, escalations []api.OperationWorkflowBlockerEscalation, opts api.OperationWorkflowAttentionOptions) bool {
	for _, b := range blockers {
		if operationAttentionBlockerMatches(b, opts) && workflowBlockerEscalated(b, escalations) {
			return true
		}
	}
	return false
}
func addWorkflowEscalationStats(stats *api.OperationWorkflowAttentionStats, blockers []api.OperationWorkflowBlocker, escalations []api.OperationWorkflowBlockerEscalation) {
	var count int64
	for _, b := range blockers {
		if workflowBlockerEscalated(b, escalations) {
			count++
		}
	}
	stats.EscalatedBlockerCount += count
	if count > 0 {
		stats.EscalatedWorkflowCount++
	}
}
