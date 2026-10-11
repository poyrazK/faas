package state

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func dependencyKey(d api.OperationWorkflowDependency) [4]string {
	return [4]string{d.SubjectType, d.SubjectID, d.Workflow, d.InstanceID}
}
func decodeWorkflowDependencies(raw []byte) []api.OperationWorkflowDependency {
	var result []api.OperationWorkflowDependency
	_ = json.Unmarshal(raw, &result)
	return result
}
func dependencyStatus(d api.OperationWorkflowDependency, state *api.OperationWorkflowState) string {
	if state == nil {
		return "unknown"
	}
	if !state.Terminal {
		return "waiting"
	}
	if d.RequiredOutcomeCode == "" {
		return "terminal"
	}
	if state.OutcomeCode == "" {
		return "unknown"
	}
	if state.OutcomeCode != d.RequiredOutcomeCode {
		return "outcome_mismatch"
	}
	return "satisfied"
}
func projectRelatedWorkflowInstances(instance *api.OperationWorkflowInstanceSnapshot, states map[[4]string]api.OperationWorkflowState) {
	if instance == nil || instance.State == nil {
		return
	}
	instance.RelatedWorkflows = make([]api.OperationWorkflowRelatedInstance, 0, len(instance.State.DependsOn))
	unresolved := 0
	for _, dependency := range instance.State.DependsOn {
		related := api.OperationWorkflowRelatedInstance{Dependency: dependency}
		if state, ok := states[dependencyKey(dependency)]; ok {
			copy := state
			related.State = &copy
		}
		related.Status = dependencyStatus(dependency, related.State)
		if related.Status == "unknown" || related.Status == "waiting" || related.Status == "outcome_mismatch" {
			unresolved++
		}
		instance.RelatedWorkflows = append(instance.RelatedWorkflows, related)
	}
	if unresolved > 0 && !instance.State.Terminal && instance.Decision != nil {
		if !instance.Decision.NeedsAttention {
			instance.Decision.Reason = "dependency_waiting"
		}
		instance.Decision.NeedsAttention = true
		instance.Decision.Explanation += " Application-reported prerequisites include unfinished, unknown, or mismatched outcomes. Verify the linked business rows before acting."
	}
}
func (m *MemStore) projectRelatedWorkflowsLocked(page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, operator bool, now time.Time) {
	instance := page.WorkflowInstance
	if instance == nil || instance.State == nil || len(instance.State.DependsOn) == 0 {
		return
	}
	if operator {
		tenant = instance.State.PlatformTenantID
	}
	if tenant == "" {
		return
	}
	data := m.operationMemoryLocked()
	states := map[[4]string]api.OperationWorkflowState{}
	for _, d := range instance.State.DependsOn {
		key := operationWorkflowStateKey(uuid.MustParse(account).String(), uuid.MustParse(opts.AppID).String(), uuid.MustParse(tenant).String(), opts.Scope, d.SubjectType, d.SubjectID, d.Workflow, d.InstanceID)
		record, ok := data.workflowStates[key]
		if !ok {
			continue
		}
		op, ok := data.operations[record.OperationID]
		if !ok || !operationRetained(op, now) {
			continue
		}
		if _, ok = data.workflowStateReports[record.OperationID+"/"+record.ReportID]; !ok {
			continue
		}
		state := record.State
		state.DependsOn = append([]api.OperationWorkflowDependency(nil), state.DependsOn...)
		state.Blockers = append([]api.OperationWorkflowBlocker(nil), state.Blockers...)
		state.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), state.BlockerResolutions...)
		state.EvidenceMilestones = append([]api.OperationWorkflowEvidenceMilestone(nil), state.EvidenceMilestones...)
		state.Stale = operationWorkflowStateIsStale(now, state)
		evaluateOperationWorkflowDeadline(now, &state)
		state.PlatformTenantID = ""
		if operator {
			state.PlatformTenantID = tenant
		}
		states[dependencyKey(d)] = state
	}
	projectRelatedWorkflowInstances(instance, states)
}

func unresolvedWorkflowDependencies(related []api.OperationWorkflowRelatedInstance) []api.OperationWorkflowRelatedInstance {
	result := make([]api.OperationWorkflowRelatedInstance, 0)
	for _, r := range related {
		if r.Status == "waiting" || r.Status == "unknown" || r.Status == "outcome_mismatch" {
			r.State = nil
			result = append(result, r)
		}
	}
	return result
}
func operationAttentionDependencyMatches(related []api.OperationWorkflowRelatedInstance, opts api.OperationWorkflowAttentionOptions) bool {
	if opts.DependencyStatus == "" && opts.RequiredOutcomeCode == "" {
		return true
	}
	for _, r := range related {
		if (opts.DependencyStatus == "" || opts.DependencyStatus == r.Status) && (opts.RequiredOutcomeCode == "" || opts.RequiredOutcomeCode == r.Dependency.RequiredOutcomeCode) {
			return true
		}
	}
	return false
}
func addOperationDependencyStats(stats *api.OperationWorkflowAttentionStats, dependencies []api.OperationWorkflowRelatedInstance) {
	if len(dependencies) > 0 {
		stats.DependencyWorkflowCount++
		stats.DependencyCount += int64(len(dependencies))
	}
}
