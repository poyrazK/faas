package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) reconcileReadinessRemindersLocked(rule AlertRule, now time.Time) {
	for id, e := range m.readinessIncidentEvents {
		i := m.readinessIncidents[e.IncidentID]
		if e.RuleID != rule.ID || i.AccountID != rule.AccountID || i.AppID != rule.AppID {
			continue
		}
		if reason := readinessReminderSuppression(i, m.readinessAttention[i.ID], e, now); reason != "" {
			if readinessPrimaryUncertain(e, now) {
				i.Routing.PrimaryInformed = true
				m.readinessIncidents[i.ID] = i
			}
			suppressReadinessEvent(&e, nil, reason)
			m.readinessIncidentEvents[id] = e
		}
	}
}
func (m *MemStore) recordReadinessAttentionLocked(rule AlertRule, i *readinessIncident, o MCPReadinessObservation, normal *readinessIncidentEventRecord, windows []api.MCPReadinessMaintenanceWindow) (*readinessIncidentEventRecord, error) {
	if i != nil {
		m.readinessAttention[i.ID] = recordReadinessAttentionObservation(m.readinessAttention[i.ID], *i, o, normal)
	}
	m.reconcileReadinessRemindersLocked(rule, o.At)
	if i == nil {
		return nil, nil
	}
	pending := false
	for _, e := range m.readinessIncidentEvents {
		inc := m.readinessIncidents[e.IncidentID]
		if e.IncidentID == i.ID && e.RuleID == rule.ID && inc.AccountID == rule.AccountID && inc.AppID == rule.AppID && !readinessNotificationTerminal(e.Status) {
			pending = true
			break
		}
	}
	r, event, err := decideReadinessReminder(rule, *i, m.readinessAttention[i.ID], o, normal, pending, windows)
	if err != nil {
		return nil, err
	}
	m.readinessAttention[i.ID] = r
	if event != nil {
		m.readinessIncidentSequence++
		event.Sequence = m.readinessIncidentSequence
		m.readinessIncidentEvents[event.ID] = *event
	}
	return event, nil
}
func (m *MemStore) ListMCPReadinessAttention(_ context.Context, account, app, scope, after string, limit int, includeResolved bool, now time.Time) (api.MCPReadinessAttentionPage, error) {
	if err := readinessNotificationListArgs(scope, after, limit); err != nil {
		return api.MCPReadinessAttentionPage{}, err
	}
	if after != "" {
		after = uuid.MustParse(after).String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessAttentionPage{}, ErrNotFound
	}
	entries := []readinessIncident{}
	for _, i := range m.readinessIncidents {
		rule, ok := m.alertRules[i.RuleID]
		if !ok || rule.AccountID != account || rule.AppID != app {
			continue
		}
		if (includeResolved || i.ResolvedAt == nil) && i.AccountID == account && i.AppID == app && (scope == "" || i.Scope == "" || i.Scope == scope) && i.ID > after {
			entries = append(entries, i)
		}
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].ID < entries[b].ID })
	out := api.MCPReadinessAttentionPage{Incidents: []api.MCPReadinessIncidentAttention{}}
	if len(entries) > limit {
		entries = entries[:limit]
		out.NextAfter = entries[limit-1].ID
	}
	for _, i := range entries {
		out.Incidents = append(out.Incidents, readinessAttentionView(i, m.alertRules[i.RuleID], m.readinessAttention[i.ID], now))
	}
	return out, nil
}
func (m *MemStore) ActMCPReadinessAttention(_ context.Context, account, app, id string, request api.MCPReadinessAttentionAction, now time.Time) (api.MCPReadinessAttentionReceipt, error) {
	request, err := canonicalReadinessAttentionAction(request)
	if err != nil || uuid.Validate(id) != nil {
		return api.MCPReadinessAttentionReceipt{}, ErrInvalidArgument
	}
	id = uuid.MustParse(id).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessAttentionReceipt{}, ErrNotFound
	}
	key := app + ":" + request.RequestID
	if old, ok := m.readinessAttentionActions[key]; ok {
		if old.AccountID != account {
			return api.MCPReadinessAttentionReceipt{}, ErrNotFound
		}
		if old.IncidentID != id || !sameReadinessAttentionAction(old.Request, request) {
			return api.MCPReadinessAttentionReceipt{}, ErrConflict
		}
		return cloneReadiness(old.Receipt), nil
	}
	i, ok := m.readinessIncidents[id]
	if !ok || i.AccountID != account || i.AppID != app {
		return api.MCPReadinessAttentionReceipt{}, ErrNotFound
	}
	rule, ok := m.alertRules[i.RuleID]
	if !ok || rule.AccountID != account || rule.AppID != app {
		return api.MCPReadinessAttentionReceipt{}, ErrNotFound
	}
	if string(rule.Metric) != api.MetricMCPReadinessUnhealthy || rule.Action != AlertActionWebhook {
		return api.MCPReadinessAttentionReceipt{}, ErrConflict
	}
	r, receipt, err := decideReadinessAttentionAction(i, m.readinessAttention[id], request, now)
	if err != nil {
		return receipt, err
	}
	m.readinessAttention[id] = r
	m.readinessAttentionActions[key] = readinessAttentionActionRecord{AccountID: account, AppID: app, IncidentID: id, Request: request, Receipt: receipt}
	m.reconcileReadinessRemindersLocked(rule, now)
	return cloneReadiness(receipt), nil
}
func (m *MemStore) ListMCPReadinessAttentionHistory(_ context.Context, account, app, id, before string, limit int) (api.MCPReadinessAttentionHistory, error) {
	at, key, err := validateReadinessIncidentList("", before, limit)
	if err != nil || uuid.Validate(id) != nil {
		return api.MCPReadinessAttentionHistory{}, ErrInvalidArgument
	}
	id = uuid.MustParse(id).String()
	if key != "" {
		key = uuid.MustParse(key).String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.readinessIncidents[id]
	if !m.readinessAppLocked(account, app) || !ok || i.AccountID != account || i.AppID != app {
		return api.MCPReadinessAttentionHistory{}, ErrNotFound
	}
	out := api.MCPReadinessAttentionHistory{Actions: []api.MCPReadinessAttentionReceipt{}}
	for _, r := range m.readinessAttentionActions {
		v := r.Receipt
		if r.AccountID == account && r.AppID == app && r.IncidentID == id && (at.IsZero() || v.CreatedAt.Before(at) || v.CreatedAt.Equal(at) && v.RequestID < key) {
			out.Actions = append(out.Actions, cloneReadiness(v))
		}
	}
	sort.Slice(out.Actions, func(a, b int) bool {
		if out.Actions[a].CreatedAt.Equal(out.Actions[b].CreatedAt) {
			return out.Actions[a].RequestID > out.Actions[b].RequestID
		}
		return out.Actions[a].CreatedAt.After(out.Actions[b].CreatedAt)
	})
	if len(out.Actions) > limit {
		out.Actions = out.Actions[:limit]
		v := out.Actions[limit-1]
		out.NextBefore = api.MCPReadinessIncidentCursor(v.CreatedAt, v.RequestID)
	}
	return out, nil
}
