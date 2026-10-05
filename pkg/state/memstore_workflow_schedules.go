package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

func (m *MemStore) workflowScheduleTargetLocked(appID string) (Deployment, Account, bool) {
	app, exists := m.apps[appID]
	if !exists || app.Status == AppDeleted || app.MaintenanceMode || app.PlatformTenantRequired {
		return Deployment{}, Account{}, false
	}
	account, exists := m.accounts[app.AccountID]
	if !exists || !account.Active() || !account.Plan.WorkflowsAllowed() {
		return Deployment{}, Account{}, false
	}
	deployment := m.automationDeploymentLocked(appID)
	effective, err := mergeAutomationDefinitions(deployment.Workflows, m.automationRecordsLocked(appID))
	if err != nil {
		return Deployment{}, Account{}, false
	}
	deployment.Workflows = effective
	return deployment, account, deployment.ID != ""
}

func (m *MemStore) ListWorkflowScheduleCandidates(_ context.Context, owner, after string, limit int) ([]WorkflowScheduleCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]WorkflowScheduleCandidate, 0)
	for id, app := range m.apps {
		if id <= after || (owner != "" && app.NodeID != owner) {
			continue
		}
		deployment, _, eligible := m.workflowScheduleTargetLocked(id)
		if !eligible {
			continue
		}
		var definitions []struct{ Trigger *struct{ Type string } }
		if json.Unmarshal(deployment.Workflows, &definitions) != nil {
			continue
		}
		for _, definition := range definitions {
			if definition.Trigger != nil && definition.Trigger.Type == "schedule" {
				result = append(result, WorkflowScheduleCandidate{AppID: id, DeploymentID: deployment.ID, Workflows: cloneWorkflowJSON(deployment.Workflows)})
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AppID < result[j].AppID })
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemStore) AdmitScheduledWorkflow(_ context.Context, appID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deployment, account, eligible := m.workflowScheduleTargetLocked(appID)
	if !eligible || deployment.ID != deploymentID {
		return WorkflowScheduleCursor{}, false, nil
	}
	definition, err := scheduledWorkflowDefinition(deployment.Workflows, name, account.Plan)
	if err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	var definitions []struct {
		Name    string
		Trigger *struct{ Type string }
	}
	if err := json.Unmarshal(deployment.Workflows, &definitions); err != nil {
		return WorkflowScheduleCursor{}, false, err
	}
	names := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		if definition.Trigger != nil && definition.Trigger.Type == "schedule" {
			names[definition.Name] = true
		}
	}
	for key, cursor := range m.workflowSchedules {
		if cursor.AppID == appID && !names[cursor.WorkflowName] {
			delete(m.workflowSchedules, key)
		}
	}
	if definition == nil {
		return WorkflowScheduleCursor{}, false, nil
	}
	active, named := 0, 0
	for _, run := range m.workflowRuns {
		if run.AppID == appID && (run.Status == WorkflowRunStatusPending || run.Status == WorkflowRunStatusRunning || run.Status == WorkflowRunStatusAwaitingEvent) {
			active++
			if run.WorkflowName == name {
				named++
			}
		}
	}
	key := appID + "/" + name
	var previous *WorkflowScheduleCursor
	if stored, exists := m.workflowSchedules[key]; exists {
		previous = &stored
	}
	cursor, run, err := evaluateWorkflowSchedule(appID, deploymentID, *definition, previous, now, active, named, account.Plan.WorkflowMaxConcurrentRuns())
	if err != nil || cursor == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	if run != nil {
		if err := m.insertWorkflowRunLocked(run); err != nil {
			return WorkflowScheduleCursor{}, false, err
		}
	}
	if m.workflowSchedules == nil {
		m.workflowSchedules = make(map[string]WorkflowScheduleCursor)
	}
	m.workflowSchedules[key] = *cursor
	return *cursor, true, nil
}

func (m *MemStore) ListWorkflowScheduleCursors(_ context.Context, appID string) ([]WorkflowScheduleCursor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]WorkflowScheduleCursor, 0)
	for _, cursor := range m.workflowSchedules {
		if cursor.AppID != appID {
			continue
		}
		cursor.TriggerSnapshot = cloneWorkflowJSON(cursor.TriggerSnapshot)
		cursor.ScheduledFor = cloneTimePtr(cursor.ScheduledFor)
		if _, exists := m.workflowRuns[cursor.LastRunID]; !exists {
			cursor.LastRunID = ""
		}
		result = append(result, cursor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkflowName < result[j].WorkflowName })
	return result, nil
}
