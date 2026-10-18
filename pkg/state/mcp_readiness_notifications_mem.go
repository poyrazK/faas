package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) ListMCPReadinessNotificationQueues(_ context.Context, account, app, scope, after string, limit int, now time.Time) (api.MCPReadinessNotificationQueues, error) {
	if err := readinessNotificationListArgs(scope, after, limit); err != nil {
		return api.MCPReadinessNotificationQueues{}, err
	}
	if after != "" {
		after = uuid.MustParse(after).String()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessNotificationQueues{}, ErrNotFound
	}
	rules := []AlertRule{}
	for _, r := range m.alertRules {
		if r.AccountID == account && r.AppID == app && string(r.Metric) == api.MetricMCPReadinessUnhealthy && r.Action == AlertActionWebhook && (scope == "" || r.MCPReadinessScope == "" || r.MCPReadinessScope == scope) && canonicalMemUUID(r.ID) > after {
			rules = append(rules, r)
		}
	}
	sort.Slice(rules, func(i, j int) bool { return canonicalMemUUID(rules[i].ID) < canonicalMemUUID(rules[j].ID) })
	out := api.MCPReadinessNotificationQueues{ObservedAt: now, Queues: []api.MCPReadinessNotificationQueue{}}
	if len(rules) > limit {
		rules = rules[:limit]
		out.NextAfter = canonicalMemUUID(rules[limit-1].ID)
	}
	windows := m.readinessMaintenanceWindowsLocked(account, app)
	for _, r := range rules {
		events, incidents := m.readinessOutstandingLocked(r)
		out.Queues = append(out.Queues, readinessNotificationQueues(r, m.readinessDeliveryPolicyLocked(r.ID), events, incidents, now, windows))
	}
	return out, nil
}
func (m *MemStore) ActMCPReadinessNotification(_ context.Context, account, app string, r api.MCPReadinessNotificationAction, now time.Time) (api.MCPReadinessNotificationReceipt, error) {
	r, err := canonicalReadinessNotificationAction(r)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessNotificationReceipt{}, ErrNotFound
	}
	key := app + ":" + r.RequestID
	if old, ok := m.readinessNotificationActions[key]; ok {
		if old.AccountID != account {
			return api.MCPReadinessNotificationReceipt{}, ErrNotFound
		}
		if !sameReadinessNotificationAction(old.Request, r) {
			return api.MCPReadinessNotificationReceipt{}, ErrConflict
		}
		return old.Receipt, nil
	}
	e, ok := m.readinessIncidentEvents[r.EventID]
	inc := m.readinessIncidents[e.IncidentID]
	if !ok || inc.AccountID != account || inc.AppID != app {
		return api.MCPReadinessNotificationReceipt{}, ErrNotFound
	}
	rule, ok := m.alertRules[e.RuleID]
	if !ok || rule.AccountID != account || rule.AppID != app {
		return api.MCPReadinessNotificationReceipt{}, ErrNotFound
	}
	events, incidents := m.readinessOutstandingLocked(rule)
	e, err = readinessNotificationActionEvent(rule, m.readinessDeliveryPolicyLocked(rule.ID), events, incidents, r)
	if err != nil {
		return api.MCPReadinessNotificationReceipt{}, err
	}
	uncertain := readinessActionPrimaryUncertain(e, r, now)
	e, receipt, err := decideReadinessNotificationAction(rule, e, r, now, m.readinessMaintenanceWindowsLocked(account, app))
	if err != nil {
		return receipt, err
	}
	if uncertain {
		inc.Routing.PrimaryInformed = true
		m.readinessIncidents[inc.ID] = inc
	}
	m.readinessIncidentEvents[e.ID] = e
	m.readinessNotificationActions[key] = readinessNotificationActionRecord{AccountID: account, Request: r, Receipt: receipt}
	return receipt, nil
}
