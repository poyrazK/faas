package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ RouteMonitorRollbackStore = (*MemStore)(nil)

// ClaimRouteMonitorRollback mirrors Postgres for the app's open incident
// (ADR-845). MemStore does not open incidents from telemetry, so tests seed
// them directly.
func (m *MemStore) ClaimRouteMonitorRollback(_ context.Context, accountID, appID string) (RouteMonitorRollbackClaim, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.routeMonitorConfigLocked(accountID, appID)
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	incidents := m.routeMonitorIncidents[appID]
	for j := len(incidents) - 1; j >= 0; j-- {
		if incidents[j].Status != "open" {
			continue
		}
		d, ok := m.deployments[incidents[j].DeploymentID]
		if !ok {
			return RouteMonitorRollbackClaim{}, false, nil
		}
		var promotedAt *time.Time
		if at, ok := m.deploymentPromotedAt[d.ID]; ok {
			promotedAt = &at
		}
		claim, claimed, _ := decideRouteMonitorRollback(c, &incidents[j], d, promotedAt, time.Now().UTC())
		return claim, claimed, nil
	}
	return RouteMonitorRollbackClaim{}, false, nil
}

// markDeploymentPromotedLocked mirrors deployments.promoted_at: a traffic
// change took id to 100% (ADR-20261028110044 amendment 4). Caller holds mu.
func (m *MemStore) markDeploymentPromotedLocked(id string, at time.Time) {
	if m.deploymentPromotedAt == nil {
		m.deploymentPromotedAt = map[string]time.Time{}
	}
	m.deploymentPromotedAt[id] = at
}

func (m *MemStore) RecordRouteMonitorRollback(_ context.Context, accountID, appID, incidentID string, outcome api.RouteMonitorIncidentRollback) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.monitorIncidentAccessLocked(accountID, appID); err != nil {
		return err
	}
	for j := range m.routeMonitorIncidents[appID] {
		if i := &m.routeMonitorIncidents[appID][j]; i.ID == incidentID {
			return recordRouteMonitorRollback(i, outcome)
		}
	}
	return ErrNotFound
}

// SeedRouteMonitorIncidentForTest stores an incident as evaluation would.
// MemStore has no request telemetry to open one itself.
func (m *MemStore) SeedRouteMonitorIncidentForTest(appID string, i api.RouteMonitorIncident) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.routeMonitorIncidents == nil {
		m.routeMonitorIncidents = map[string][]api.RouteMonitorIncident{}
	}
	m.routeMonitorIncidents[appID] = append(m.routeMonitorIncidents[appID], i)
}
