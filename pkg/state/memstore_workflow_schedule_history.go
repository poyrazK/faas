package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) recordWorkflowScheduleOccurrenceLocked(cursor, previous *WorkflowScheduleCursor, definition api.WorkflowSpec) {
	occurrence := workflowScheduleOccurrence(cursor, previous, definition)
	if occurrence == nil {
		return
	}
	if m.workflowScheduleOccurrences == nil {
		m.workflowScheduleOccurrences = make(map[string]WorkflowScheduleOccurrence)
	}
	m.workflowScheduleOccurrences[occurrence.ID] = *occurrence
}

func (m *MemStore) ListWorkflowScheduleOccurrences(_ context.Context, appID, tenantID, before string, limit int) ([]WorkflowScheduleOccurrence, error) {
	if limit <= 0 || limit > api.WorkflowScheduleHistoryPageMax+1 {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var boundary *WorkflowScheduleOccurrence
	if before != "" {
		row, ok := m.workflowScheduleOccurrences[before]
		if !ok || row.AppID != appID || tenantID != "" && row.PlatformTenantID != tenantID {
			return []WorkflowScheduleOccurrence{}, nil
		}
		boundary = &row
	}
	result := make([]WorkflowScheduleOccurrence, 0)
	for _, row := range m.workflowScheduleOccurrences {
		if row.AppID != appID || tenantID != "" && row.PlatformTenantID != tenantID {
			continue
		}
		if boundary != nil && !workflowOccurrenceBefore(row, *boundary) {
			continue
		}
		result = append(result, row)
	}
	sort.Slice(result, func(i, j int) bool { return workflowOccurrenceBefore(result[j], result[i]) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func workflowOccurrenceBefore(a, b WorkflowScheduleOccurrence) bool {
	if !a.ScheduledFor.Equal(b.ScheduledFor) {
		return a.ScheduledFor.Before(b.ScheduledFor)
	}
	return a.ID < b.ID
}

func (m *MemStore) PruneWorkflowScheduleOccurrences(_ context.Context, before time.Time, limit int) (int, error) {
	if limit <= 0 || limit > api.WorkflowScheduleHistoryPruneBatch {
		return 0, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]WorkflowScheduleOccurrence, 0)
	for _, row := range m.workflowScheduleOccurrences {
		if row.EvaluatedAt.Before(before) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].EvaluatedAt.Equal(rows[j].EvaluatedAt) {
			return rows[i].EvaluatedAt.Before(rows[j].EvaluatedAt)
		}
		return rows[i].ID < rows[j].ID
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	for _, row := range rows {
		delete(m.workflowScheduleOccurrences, row.ID)
	}
	return len(rows), nil
}

func (m *MemStore) PreviewWorkflowScheduleReplays(_ context.Context, appID string, ids []string) ([]WorkflowScheduleReplayResult, error) {
	return m.workflowScheduleReplayResults(appID, ids, false)
}

func (m *MemStore) ReplayWorkflowScheduleOccurrences(_ context.Context, appID string, ids []string) ([]WorkflowScheduleReplayResult, error) {
	return m.workflowScheduleReplayResults(appID, ids, true)
}

func (m *MemStore) workflowScheduleReplayResults(appID string, ids []string, execute bool) ([]WorkflowScheduleReplayResult, error) {
	if err := validateWorkflowScheduleReplaySelection(appID, ids); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	rows := make([]WorkflowScheduleOccurrence, 0, len(ids))
	for _, id := range ids {
		row, exists := m.workflowScheduleOccurrences[id]
		if !exists || row.AppID != appID {
			rows = append(rows, WorkflowScheduleOccurrence{ID: id})
			continue
		}
		rows = append(rows, row)
	}
	workflowScheduleReplayOrder(rows)

	active := 0
	for _, run := range m.workflowRuns {
		if run.AppID == appID && workflowRunIsActive(run.Status) {
			active++
		}
	}
	reserved := 0
	reservedNamed := make(map[string]int)
	results := make([]WorkflowScheduleReplayResult, 0, len(rows))
	for _, row := range rows {
		if row.AppID == "" {
			results = append(results, WorkflowScheduleReplayResult{OccurrenceID: row.ID, Outcome: WorkflowScheduleReplayOccurrenceNotFound})
			continue
		}
		initial := workflowScheduleReplayInitialOutcome(row)
		result := workflowScheduleReplayResult(row, initial)
		if initial != WorkflowScheduleReplayEligible {
			results = append(results, result)
			continue
		}

		var deployment Deployment
		var account Account
		var eligible bool
		var scheduleCursor *WorkflowScheduleCursor
		if row.PlatformTenantID != "" {
			deployment, account, eligible = m.tenantWorkflowScheduleTargetLocked(appID, row.PlatformTenantID)
			if !eligible {
				result.Outcome = WorkflowScheduleReplayTenantUnavailable
				results = append(results, result)
				continue
			}
			if cursor, exists := m.workflowTenantSchedules[appID+"/"+row.PlatformTenantID+"/"+row.WorkflowName]; exists {
				copy := cursor
				scheduleCursor = &copy
			}
		} else {
			deployment, account, eligible = m.workflowScheduleTargetLocked(appID)
			if !eligible {
				result.Outcome = WorkflowScheduleReplayTargetUnavailable
				results = append(results, result)
				continue
			}
		}
		if deployment.ID != row.DeploymentID {
			result.Outcome = WorkflowScheduleReplayDeploymentChanged
			results = append(results, result)
			continue
		}
		if !account.Plan.WorkflowsAllowed() {
			result.Outcome = WorkflowScheduleReplayPlanUnavailable
			results = append(results, result)
			continue
		}
		definition, outcome, err := workflowScheduleReplayDefinition(row, deployment.Workflows, account.Plan, scheduleCursor)
		if err != nil {
			return nil, err
		}
		if outcome != WorkflowScheduleReplayEligible {
			result.Outcome = outcome
			results = append(results, result)
			continue
		}
		named := 0
		for _, run := range m.workflowRuns {
			if run.AppID != appID || !workflowRunIsActive(run.Status) || run.WorkflowName != row.WorkflowName ||
				!sameMemUUID(run.PlatformTenantID, row.PlatformTenantID) {
				continue
			}
			named++
		}
		admissionKey := row.PlatformTenantID + "/" + row.WorkflowName
		named += reservedNamed[admissionKey]
		if definition.Trigger.Overlap != "allow" && named > 0 {
			result.Outcome = WorkflowScheduleReplayOverlapActive
			results = append(results, result)
			continue
		}
		if active+reserved >= account.Plan.WorkflowMaxConcurrentRuns() {
			result.Outcome = WorkflowScheduleReplayQuotaFull
			results = append(results, result)
			continue
		}
		if !execute {
			reserved++
			reservedNamed[admissionKey]++
			results = append(results, result)
			continue
		}

		definitionSnapshot, err := json.Marshal(definition)
		if err != nil {
			return nil, fmt.Errorf("state: encode replay workflow definition: %w", err)
		}
		run := &WorkflowRun{AppID: appID, DeploymentID: deployment.ID, PlatformTenantID: row.PlatformTenantID,
			WorkflowName: row.WorkflowName, DefinitionSnapshot: definitionSnapshot, Input: cloneWorkflowJSON(definition.Trigger.Input),
			ScheduledFor: row.ScheduledFor}
		if err := prepareWorkflowRun(run); err != nil {
			return nil, err
		}
		if err := m.insertWorkflowRunLocked(run); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		row.ReplayRunID, row.ReplayedAt = run.ID, &now
		m.workflowScheduleOccurrences[row.ID] = row
		admitted := now.Truncate(time.Minute)
		if row.PlatformTenantID != "" {
			key := appID + "/" + row.PlatformTenantID + "/" + row.WorkflowName
			if cursor, exists := m.workflowTenantSchedules[key]; exists {
				cursor.LastAdmittedAt = &admitted
				m.workflowTenantSchedules[key] = cursor
			}
		} else {
			key := appID + "/" + row.WorkflowName
			if cursor, exists := m.workflowSchedules[key]; exists {
				cursor.LastAdmittedAt = &admitted
				m.workflowSchedules[key] = cursor
			}
		}
		result.Outcome, result.ReplayRunID = WorkflowScheduleReplayReplayed, run.ID
		results = append(results, result)
	}
	return results, nil
}

func workflowRunIsActive(status string) bool {
	return status == WorkflowRunStatusPending || status == WorkflowRunStatusRunning || status == WorkflowRunStatusAwaitingEvent
}

func (m *MemStore) ListFairTenantWorkflowScheduleCandidates(ctx context.Context, owner string, minute time.Time, limit int) ([]WorkflowScheduleCandidate, error) {
	if limit <= 0 || limit > api.WorkflowScheduleBatch {
		return nil, ErrInvalidArgument
	}
	candidates, err := m.ListTenantWorkflowScheduleCandidates(ctx, owner, "", "", 0)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	type entry struct {
		candidate WorkflowScheduleCandidate
		name      string
		admitted  time.Time
	}
	entries := make([]entry, 0)
	for _, candidate := range candidates {
		var definitions []api.WorkflowSpec
		if err := json.Unmarshal(candidate.Workflows, &definitions); err != nil {
			return nil, err
		}
		for _, definition := range definitions {
			trigger := definition.Trigger
			if trigger == nil || trigger.Type != "schedule" || trigger.Enabled != nil && !*trigger.Enabled {
				continue
			}
			cursor := m.workflowTenantSchedules[candidate.AppID+"/"+candidate.PlatformTenantID+"/"+definition.Name]
			if !cursor.LastEvaluatedAt.Before(minute) {
				continue
			}
			configured, _, customized := tenantWorkflowScheduleConfigFromSnapshot(cursor.TriggerSnapshot)
			if trigger.TenantConfigurable && customized && configured.Enabled != nil && !*configured.Enabled {
				continue
			}
			raw, err := json.Marshal([]api.WorkflowSpec{definition})
			if err != nil {
				return nil, err
			}
			value := candidate
			value.Workflows = raw
			row := entry{candidate: value, name: definition.Name}
			if cursor.LastAdmittedAt != nil {
				row.admitted = *cursor.LastAdmittedAt
			}
			entries = append(entries, row)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if !a.admitted.Equal(b.admitted) {
			return a.admitted.Before(b.admitted)
		}
		if a.candidate.AppID != b.candidate.AppID {
			return a.candidate.AppID < b.candidate.AppID
		}
		if a.candidate.PlatformTenantID != b.candidate.PlatformTenantID {
			return a.candidate.PlatformTenantID < b.candidate.PlatformTenantID
		}
		return a.name < b.name
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	result := make([]WorkflowScheduleCandidate, 0, len(entries))
	for _, row := range entries {
		result = append(result, row.candidate)
	}
	return result, nil
}
