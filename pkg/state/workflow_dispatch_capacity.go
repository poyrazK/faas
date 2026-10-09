package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowDefinitionScope struct{ appID, name string }

type workflowDispatchOccupancy struct {
	apps        map[string]int
	tenants     map[workflowDispatchScope]int
	definitions map[workflowDefinitionScope]int
}

// Shared by admission and diagnostics so parked waits occupy definition
// capacity while only unexpired running leases occupy dispatch capacity.
func (m *MemStore) workflowDispatchOccupancyLocked(now time.Time) workflowDispatchOccupancy {
	counts := workflowDispatchOccupancy{make(map[string]int), make(map[workflowDispatchScope]int), make(map[workflowDefinitionScope]int)}
	for _, run := range m.workflowRuns {
		live := run.Status == WorkflowRunStatusRunning && m.workflowDispatchDeadlineLocked(run).After(now)
		if live {
			if _, operation := m.operationForWorkflowLocked(run.ID); !operation {
				counts.apps[run.AppID]++
				counts.tenants[workflowDispatchScope{run.AppID, run.PlatformTenantID}]++
			}
		}
		if live || run.Status == WorkflowRunStatusAwaitingEvent || (run.Status == WorkflowRunStatusPending && run.StartedAt != nil) {
			counts.definitions[workflowDefinitionScope{run.AppID, run.WorkflowName}]++
		}
	}
	return counts
}

func (counts workflowDispatchOccupancy) capacityReason(run WorkflowRun) string {
	return counts.capacityReasonFor(run, false)
}

// Operation workflows share per-definition concurrency with native runs but
// are admitted and dispatched under the Operations plan. They therefore do
// not consume the legacy app and tenant dispatch slots.
func (counts workflowDispatchOccupancy) capacityReasonFor(run WorkflowRun, operation bool) string {
	if !operation {
		if counts.apps[run.AppID] >= api.WorkflowDispatchMaxPerApp {
			return api.AutomationQueueAppCapacity
		}
		if run.PlatformTenantID != "" && counts.tenants[workflowDispatchScope{run.AppID, run.PlatformTenantID}] >= api.WorkflowDispatchMaxPerTenant {
			return api.AutomationQueueTenantCapacity
		}
	}
	active := counts.definitions[workflowDefinitionScope{run.AppID, run.WorkflowName}]
	if run.Status == WorkflowRunStatusAwaitingEvent || (run.Status == WorkflowRunStatusPending && run.StartedAt != nil) {
		active--
	}
	if limit := workflowRunMaxConcurrentRuns(run.DefinitionSnapshot); limit > 0 && active >= limit {
		return api.AutomationQueueWorkflowCapacity
	}
	return ""
}
