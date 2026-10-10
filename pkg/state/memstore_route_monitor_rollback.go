package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ RouteMonitorRollbackStore = (*MemStore)(nil)

// ClaimRouteMonitorRollback mirrors Postgres for the app's open incident
// (ADR-943). MemStore does not open incidents from telemetry, so tests seed
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
		claim, claimed, _ := decideRouteMonitorRollback(c, &incidents[j], d, time.Now().UTC())
		return claim, claimed, nil
	}
	return RouteMonitorRollbackClaim{}, false, nil
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
