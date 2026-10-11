package state

import (
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const operationDependencyImpactLimit = 100

// Account reads use the selected state's customer; an unknown state requires
// an explicit customer selector. Self reads always use the authenticated owner.
func operationDependencyImpactTenant(instance *api.OperationWorkflowInstanceSnapshot, tenant string, opts api.OperationMilestoneListOptions, operator bool) string {
	if instance == nil || opts.SubjectType == "" || opts.SubjectID == "" {
		return ""
	}
	if !operator {
		return tenant
	}
	if instance.State != nil {
		return instance.State.PlatformTenantID
	}
	return opts.TenantID
}

func (m *MemStore) projectDependencyImpactLocked(page *api.OperationMilestonesResponse, account, tenant string, opts api.OperationMilestoneListOptions, operator bool, now time.Time) {
	instance := page.WorkflowInstance
	tenant = operationDependencyImpactTenant(instance, tenant, opts, operator)
	if tenant == "" {
		return
	}
	selected := api.OperationWorkflowDependency{SubjectType: opts.SubjectType, SubjectID: opts.SubjectID, Workflow: instance.Workflow, InstanceID: instance.InstanceID}
	data := m.operationMemoryLocked()
	impact := &api.OperationWorkflowDependencyImpact{Items: make([]api.OperationWorkflowDependentInstance, 0)}
	for _, record := range data.workflowStates {
		if !sameOperationHistoryIdentity(record.AccountID, account) || !sameOperationHistoryIdentity(record.AppID, opts.AppID) || !sameOperationHistoryIdentity(record.TenantID, tenant) || record.Scope != opts.Scope {
			continue
		}
		op, ok := data.operations[record.OperationID]
		if !ok || !operationRetained(op, now) {
			continue
		}
		if _, ok = data.workflowStateReports[record.OperationID+"/"+record.ReportID]; !ok {
			continue
		}
		for _, dependency := range record.State.DependsOn {
			if dependencyKey(dependency) != dependencyKey(selected) {
				continue
			}
			state := record.State
			state.Stale = operationWorkflowStateIsStale(now, state)
			evaluateOperationWorkflowDeadline(now, &state)
			state.PlatformTenantID = ""
			if operator {
				state.PlatformTenantID = tenant
			}
			status := dependencyStatus(dependency, instance.State)
			affected := !state.Terminal && (status == "unknown" || status == "waiting" || status == "outcome_mismatch")
			impact.WorkflowCount++
			if affected {
				impact.ImpactedWorkflowCount++
			}
			item := api.OperationWorkflowDependentInstance{Subject: api.OperationSubject{Type: record.SubjectType, ID: record.SubjectID}, State: state, RequiredOutcomeCode: dependency.RequiredOutcomeCode, DependencyStatus: status, NeedsAttention: affected}
			index := sort.Search(len(impact.Items), func(i int) bool { return operationDependentInstanceLess(item, impact.Items[i]) })
			if index < operationDependencyImpactLimit {
				state.DependsOn = append([]api.OperationWorkflowDependency(nil), state.DependsOn...)
				state.Blockers = append([]api.OperationWorkflowBlocker(nil), state.Blockers...)
				state.BlockerResolutions = append([]api.OperationWorkflowBlockerResolution(nil), state.BlockerResolutions...)
				state.EvidenceMilestones = append([]api.OperationWorkflowEvidenceMilestone(nil), state.EvidenceMilestones...)
				item.State = state
				impact.Items = append(impact.Items, api.OperationWorkflowDependentInstance{})
				copy(impact.Items[index+1:], impact.Items[index:])
				impact.Items[index] = item
				if len(impact.Items) > operationDependencyImpactLimit {
					impact.Items = impact.Items[:operationDependencyImpactLimit]
				}
			}
			break
		}
	}
	impact.HasMore = impact.WorkflowCount > int64(operationDependencyImpactLimit)
	instance.DependencyImpact = impact
}

func operationDependentInstanceLess(a, b api.OperationWorkflowDependentInstance) bool {
	if a.NeedsAttention != b.NeedsAttention {
		return a.NeedsAttention
	}
	if !a.State.UpdatedAt.Equal(b.State.UpdatedAt) {
		return a.State.UpdatedAt.After(b.State.UpdatedAt)
	}
	ak, bk := dependencyKey(api.OperationWorkflowDependency{SubjectType: a.Subject.Type, SubjectID: a.Subject.ID, Workflow: a.State.Workflow, InstanceID: a.State.InstanceID}), dependencyKey(api.OperationWorkflowDependency{SubjectType: b.Subject.Type, SubjectID: b.Subject.ID, Workflow: b.State.Workflow, InstanceID: b.State.InstanceID})
	for k := range ak {
		if ak[k] != bk[k] {
			return ak[k] < bk[k]
		}
	}
	return false
}
