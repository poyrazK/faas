package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

func (m *MemStore) tenantWorkflowScheduleTargetLocked(appID, tenantID string) (Deployment, Account, bool) {
	app, exists := m.apps[appID]
	if !exists || app.Status == AppDeleted || app.MaintenanceMode || !app.PlatformTenantRequired ||
		!m.workflowOutboundTenantLinkActiveLocked(app.AccountID, tenantID, appID) {
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
	cursor, run, err := evaluateWorkflowSchedule(appID, "", deploymentID, *definition, previous, now, active, named, account.Plan.WorkflowMaxConcurrentRuns())
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
	m.recordWorkflowScheduleOccurrenceLocked(cursor, now)
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
		cursor.LastAdmittedAt = cloneTimePtr(cursor.LastAdmittedAt)
		if _, exists := m.workflowRuns[cursor.LastRunID]; !exists {
			cursor.LastRunID = ""
		}
		result = append(result, cursor)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkflowName < result[j].WorkflowName })
	return result, nil
}

func (m *MemStore) ListTenantWorkflowScheduleCandidates(_ context.Context, owner, afterApp, afterTenant string, limit int) ([]WorkflowScheduleCandidate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]WorkflowScheduleCandidate, 0)
	for appID, app := range m.apps {
		if !app.PlatformTenantRequired || owner != "" && app.NodeID != owner {
			continue
		}
		for tenantID, tenant := range m.platformTenants {
			if tenant.Status != PlatformTenantActive || afterApp != "" && (appID < afterApp || appID == afterApp && tenantID <= afterTenant) {
				continue
			}
			deployment, account, eligible := m.tenantWorkflowScheduleTargetLocked(appID, tenantID)
			if !eligible {
				continue
			}
			var definitions []api.WorkflowSpec
			if json.Unmarshal(deployment.Workflows, &definitions) != nil {
				continue
			}
			for _, definition := range definitions {
				if definition.Trigger == nil || definition.Trigger.Type != "schedule" ||
					definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled {
					continue
				}
				if _, err := api.ValidateWorkflowDAG(definition, account.Plan); err != nil {
					continue
				}
				result = append(result, WorkflowScheduleCandidate{AppID: appID, PlatformTenantID: tenantID,
					DeploymentID: deployment.ID, Workflows: cloneWorkflowJSON(deployment.Workflows)})
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].AppID != result[j].AppID {
			return result[i].AppID < result[j].AppID
		}
		return result[i].PlatformTenantID < result[j].PlatformTenantID
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemStore) AdmitTenantScheduledWorkflow(_ context.Context, appID, tenantID, deploymentID, name string, now time.Time) (WorkflowScheduleCursor, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deployment, account, eligible := m.tenantWorkflowScheduleTargetLocked(appID, tenantID)
	if !eligible || deployment.ID != deploymentID {
		return WorkflowScheduleCursor{}, false, nil
	}
	definition, err := scheduledWorkflowDefinition(deployment.Workflows, name, account.Plan)
	if err != nil || definition == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	var configured api.WorkflowTriggerSpec
	var configVersion int64
	customized := false
	key := appID + "/" + tenantID + "/" + name
	if stored, exists := m.workflowTenantSchedules[key]; exists {
		configured, configVersion, customized = tenantWorkflowScheduleConfigFromSnapshot(stored.TriggerSnapshot)
		if customized && definition.Trigger.TenantConfigurable {
			effectiveTrigger := applyTenantWorkflowScheduleTrigger(*definition.Trigger, configured)
			if effectiveTrigger.Enabled != nil && !*effectiveTrigger.Enabled {
				return WorkflowScheduleCursor{}, false, nil
			}
			definition.Trigger = &effectiveTrigger
		} else {
			customized = false
		}
	}
	active, named := 0, 0
	for _, run := range m.workflowRuns {
		if run.AppID != appID || run.Status != WorkflowRunStatusPending && run.Status != WorkflowRunStatusRunning && run.Status != WorkflowRunStatusAwaitingEvent {
			continue
		}
		active++
		if sameMemUUID(run.PlatformTenantID, tenantID) && run.WorkflowName == name {
			named++
		}
	}
	if m.workflowTenantSchedules == nil {
		m.workflowTenantSchedules = make(map[string]WorkflowScheduleCursor)
	}
	var previous *WorkflowScheduleCursor
	if stored, exists := m.workflowTenantSchedules[key]; exists {
		value := stored
		if customized {
			value.TriggerSnapshot, err = json.Marshal(*definition.Trigger)
			if err != nil {
				return WorkflowScheduleCursor{}, false, err
			}
		}
		previous = &value
	}
	cursor, run, err := evaluateWorkflowSchedule(appID, tenantID, deploymentID, *definition, previous, now,
		active, named, account.Plan.WorkflowMaxConcurrentRuns())
	if err != nil || cursor == nil {
		return WorkflowScheduleCursor{}, false, err
	}
	outcomeChanged := workflowScheduleOutcomeChanged(cursor, previous)
	if run != nil {
		if err := m.insertWorkflowRunLocked(run); err != nil {
			return WorkflowScheduleCursor{}, false, err
		}
	}
	if customized {
		cursor.TriggerSnapshot, err = encodeTenantWorkflowScheduleSnapshot(*definition.Trigger, configVersion)
		if err != nil {
			return WorkflowScheduleCursor{}, false, err
		}
	}
	m.recordWorkflowScheduleOccurrenceLocked(cursor, now)
	m.workflowTenantSchedules[key] = *cursor
	return *cursor, outcomeChanged, nil
}

func (m *MemStore) ListTenantWorkflowSchedules(_ context.Context, accountID, tenantID, appID string) ([]TenantWorkflowSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	deployment, account, eligible := m.tenantWorkflowScheduleTargetLocked(appID, tenantID)
	if !eligible || account.ID != accountID {
		return nil, ErrNotFound
	}
	var definitions []api.WorkflowSpec
	if err := json.Unmarshal(deployment.Workflows, &definitions); err != nil {
		return nil, fmt.Errorf("state: decode tenant workflow schedules: %w", err)
	}
	result := make([]TenantWorkflowSchedule, 0)
	for _, definition := range definitions {
		if definition.Trigger == nil || definition.Trigger.Type != "schedule" || !definition.Trigger.TenantConfigurable ||
			(definition.Trigger.Enabled != nil && !*definition.Trigger.Enabled) {
			continue
		}
		if _, err := api.ValidateWorkflowDAG(definition, account.Plan); err != nil {
			return nil, err
		}
		key := appID + "/" + tenantID + "/" + definition.Name
		configured, version, customized := tenantWorkflowScheduleConfigFromSnapshot(m.workflowTenantSchedules[key].TriggerSnapshot)
		result = append(result, tenantWorkflowScheduleFromDefinition(appID, tenantID, deployment.ID,
			definition, configured, version, customized))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].WorkflowName < result[j].WorkflowName })
	return result, nil
}

func (m *MemStore) UpdateTenantWorkflowSchedule(_ context.Context, accountID, tenantID, appID, name string,
	expectedVersion int64, schedule, timezone, overlap string, enabled bool) (TenantWorkflowSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if expectedVersion < 0 {
		return TenantWorkflowSchedule{}, ErrInvalidArgument
	}
	deployment, account, eligible := m.tenantWorkflowScheduleTargetLocked(appID, tenantID)
	if !eligible || account.ID != accountID {
		return TenantWorkflowSchedule{}, ErrNotFound
	}
	definition, err := tenantConfigurableWorkflowDefinition(deployment.Workflows, name, account.Plan)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	if definition == nil {
		return TenantWorkflowSchedule{}, ErrNotFound
	}
	if m.workflowTenantSchedules == nil {
		m.workflowTenantSchedules = make(map[string]WorkflowScheduleCursor)
	}
	key := appID + "/" + tenantID + "/" + name
	cursor, exists := m.workflowTenantSchedules[key]
	currentVersion := int64(0)
	if exists {
		_, currentVersion, _ = tenantWorkflowScheduleConfigFromSnapshot(cursor.TriggerSnapshot)
	}
	if currentVersion != expectedVersion {
		return TenantWorkflowSchedule{}, ErrConflict
	}
	trigger, err := normalizeTenantWorkflowScheduleTrigger(*definition.Trigger, schedule, timezone, overlap, enabled)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	version := currentVersion + 1
	snapshot, err := encodeTenantWorkflowScheduleSnapshot(trigger, version)
	if err != nil {
		return TenantWorkflowSchedule{}, err
	}
	now := time.Now().UTC()
	if !exists {
		cursor = WorkflowScheduleCursor{AppID: appID, PlatformTenantID: tenantID, WorkflowName: name,
			Status: WorkflowScheduleArmed}
	}
	cursor.DeploymentID = deployment.ID
	cursor.TriggerSnapshot = snapshot
	cursor.LastEvaluatedAt = now
	cursor.Status = WorkflowScheduleArmed
	if cursor.ScheduledFor != nil && cursor.ScheduledFor.After(now) {
		cursor.ScheduledFor = nil
	}
	m.workflowTenantSchedules[key] = cursor
	return tenantWorkflowScheduleFromDefinition(appID, tenantID, deployment.ID, *definition, trigger, version, true), nil
}

var _ TenantWorkflowScheduleStore = (*MemStore)(nil)
