package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) readinessMaintenanceWindowsLocked(account, app string) []api.MCPReadinessMaintenanceWindow {
	out := []api.MCPReadinessMaintenanceWindow{}
	for _, r := range m.readinessMaintenance {
		if r.AccountID == account && r.AppID == app {
			out = append(out, cloneReadiness(r.Window))
		}
	}
	return out
}
func (m *MemStore) CreateMCPReadinessMaintenance(_ context.Context, account, app string, r api.CreateMCPReadinessMaintenanceRequest, now time.Time) (api.MCPReadinessMaintenanceWindow, error) {
	if api.ValidateMCPReadinessMaintenance(r, now) != nil {
		return api.MCPReadinessMaintenanceWindow{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessMaintenanceWindow{}, ErrNotFound
	}
	if r.StartsAt.IsZero() {
		r.StartsAt = now
	}
	w := api.MCPReadinessMaintenanceWindow{ID: uuid.NewString(), Scope: api.MCPConnectionScope(r.Scope), StartsAt: r.StartsAt, EndsAt: r.EndsAt, Reason: r.Reason, CreatedBy: account, CreatedAt: now}
	m.readinessMaintenance[w.ID] = readinessMaintenanceRecord{AccountID: account, AppID: app, Window: w}
	return w, nil
}
func (m *MemStore) CancelMCPReadinessMaintenance(_ context.Context, account, app, id, reason string, now time.Time) (api.MCPReadinessMaintenanceWindow, error) {
	if uuid.Validate(id) != nil || !api.ValidMCPMaintenanceReason(reason) {
		return api.MCPReadinessMaintenanceWindow{}, ErrInvalidArgument
	}
	id = uuid.MustParse(id).String()
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessMaintenanceWindow{}, ErrNotFound
	}
	entry, ok := m.readinessMaintenance[id]
	if !ok || entry.AccountID != account || entry.AppID != app {
		return api.MCPReadinessMaintenanceWindow{}, ErrNotFound
	}
	if entry.Window.CancelledAt != nil {
		return cloneReadiness(entry.Window), nil
	}
	if !now.Before(entry.Window.EndsAt) {
		return api.MCPReadinessMaintenanceWindow{}, ErrConflict
	}
	entry.Window.CancelledAt = &now
	entry.Window.CancelledBy = account
	entry.Window.CancelReason = reason
	m.readinessMaintenance[id] = entry
	return cloneReadiness(entry.Window), nil
}
func (m *MemStore) ListMCPReadinessMaintenance(_ context.Context, account, app, scope, before string, limit int) (api.MCPReadinessMaintenancePage, error) {
	at, id, err := maintenanceListArgs(scope, before, limit)
	if err != nil {
		return api.MCPReadinessMaintenancePage{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.readinessAppLocked(account, app) {
		return api.MCPReadinessMaintenancePage{}, ErrNotFound
	}
	out := api.MCPReadinessMaintenancePage{Windows: []api.MCPReadinessMaintenanceWindow{}}
	for _, w := range m.readinessMaintenanceWindowsLocked(account, app) {
		if w.Scope == api.MCPConnectionScope(scope) && (at.IsZero() || w.CreatedAt.Before(at) || w.CreatedAt.Equal(at) && w.ID < id) {
			out.Windows = append(out.Windows, w)
		}
	}
	sort.Slice(out.Windows, func(i, j int) bool {
		a, b := out.Windows[i], out.Windows[j]
		if a.CreatedAt.Equal(b.CreatedAt) {
			return a.ID > b.ID
		}
		return a.CreatedAt.After(b.CreatedAt)
	})
	if len(out.Windows) > limit {
		out.Windows = out.Windows[:limit]
		last := out.Windows[limit-1]
		out.NextBefore = api.MCPReadinessIncidentCursor(last.CreatedAt, last.ID)
	}
	return out, nil
}

// Called while holding the same mutex as observations, claims and window edits.
func (m *MemStore) suppressReadinessQueueLocked(rule AlertRule, windows []api.MCPReadinessMaintenanceWindow, now time.Time) []readinessIncidentEventRecord {
	if len(windows) == 0 {
		return nil
	}
	events := []readinessIncidentEventRecord{}
	for _, e := range m.readinessIncidentEvents {
		incident := m.readinessIncidents[e.IncidentID]
		if e.RuleID == rule.ID && incident.AccountID == rule.AccountID && incident.AppID == rule.AppID {
			events = append(events, e)
		}
	}
	for i := range events {
		e := &events[i]
		if readinessNotificationTerminal(e.Status) || e.LeaseToken != "" && now.Before(e.LeaseUntil) {
			continue
		}
		ids := readinessQueuedMaintenanceIDs(rule, *e, windows, now)
		if len(ids) == 0 {
			continue
		}
		uncertain := readinessPrimaryUncertain(*e, now)
		suppressReadinessEvent(e, ids, "maintenance")
		m.readinessIncidentEvents[e.ID] = *e
		incident := m.readinessIncidents[e.IncidentID]
		if uncertain {
			incident.Routing.PrimaryInformed = true
		}
		if incident.ResolvedAt == nil && readinessSuppressionNeedsReconciliation(rule, *e, events, windows, now) {
			incident.MaintenancePending = true
		}
		m.readinessIncidents[incident.ID] = incident
	}
	return events
}
