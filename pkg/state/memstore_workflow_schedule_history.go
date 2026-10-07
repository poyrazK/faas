package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) recordWorkflowScheduleOccurrenceLocked(cursor *WorkflowScheduleCursor, now time.Time) {
	occurrence := workflowScheduleOccurrence(cursor, now)
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
