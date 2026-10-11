package state

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RequestTelemetryRouteCustomers is a read of retained debugger telemetry,
// independent from the financial consumer usage ledger.
func (s *PgStore) RequestTelemetryRouteCustomers(ctx context.Context, arg sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	return s.appErrorsQueries().RequestTelemetryRouteCustomers(ctx, s.pool, arg)
}

func (m *MemStore) RequestTelemetryRouteCustomers(_ context.Context, _ sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error) {
	return nil, errMemStoreRequestTelemetry
}

// RouteHealthStableIDs returns at most two live, traffic-serving siblings in
// the candidate's scope. Default route-health seeding requires exactly one
// (ADR-844), matching the stable selection of the route-health report.
func (s *PgStore) RouteHealthStableIDs(ctx context.Context, appID, candidateID string) ([]string, error) {
	return (&sqlc.Queries{}).RouteHealthStableIDs(ctx, s.pool, sqlc.RouteHealthStableIDsParams{AppID: appID, DeploymentID: candidateID})
}

func (m *MemStore) RouteHealthStableIDs(_ context.Context, appID, candidateID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate, ok := m.deployments[candidateID]
	if !ok || candidate.AppID != appID {
		return nil, nil
	}
	scope := func(s string) string {
		if s == "" {
			return "default"
		}
		return s
	}
	var ids []string
	for id, d := range m.deployments {
		if id != candidateID && d.AppID == appID && d.Status == DeployLive && d.TrafficPercent > 0 && scope(d.Scope) == scope(candidate.Scope) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	if len(ids) > 2 {
		ids = ids[:2]
	}
	return ids, nil
}
