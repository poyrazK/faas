package main

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// routeHealthSeedActor attributes default selectors to the platform, never to
// a customer request.
const routeHealthSeedActor = "apid:route_health_seed"

type routeHealthStableReader interface {
	RouteHealthStableIDs(ctx context.Context, appID, candidateID string) ([]string, error)
}

// seedDefaultRouteHealthGate saves report-mode selectors for an app that has
// never configured route health, when its first canary stage advances
// (ADR-951). Seeding is advisory: report mode cannot hold a rollout, so every
// failure is logged and the advance continues. Revision 0 is the only seedable
// state; a customer who saves any configuration, including an empty selector
// list, is never re-seeded.
func (s *server) seedDefaultRouteHealthGate(ctx context.Context, acct state.Account, app state.App, candidate state.Deployment) {
	if candidate.CanaryStep != 0 || !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		return
	}
	store, ok := s.store.(state.RouteHealthStore)
	if !ok {
		return
	}
	stables, ok := s.store.(routeHealthStableReader)
	if !ok {
		return
	}
	reader, ok := s.store.(routeCustomerReader)
	if !ok {
		return
	}
	gate, err := store.GetRouteHealthGate(ctx, acct.ID, app.ID)
	if err != nil || gate.Revision != 0 {
		return
	}
	ids, err := stables.RouteHealthStableIDs(ctx, app.ID, candidate.ID)
	if err != nil || len(ids) != 1 {
		return
	}
	now := time.Now().UTC()
	from := now.Add(-api.RouteHealthSeedLookback)
	if retention := now.Add(-time.Duration(api.MustLimitsFor(acct.Plan).DebugTelemetryRetentionDays) * 24 * time.Hour); from.Before(retention) {
		from = retention
	}
	rows, err := reader.RequestTelemetryRouteCustomers(ctx, sqlc.RequestTelemetryRouteCustomersParams{
		AppID: stringToPgUUID(app.ID), AccountID: stringToPgUUID(acct.ID), DeploymentID: stringToPgUUID(ids[0]),
		SinceAt: pgtype.Timestamptz{Time: from, Valid: true}, UntilAt: pgtype.Timestamptz{Time: now, Valid: true},
		RouteLimit: api.RouteCustomerUsageMaxRoutes, CustomerLimit: api.RouteCustomerUsageMaxCustomers,
	})
	if err != nil {
		s.log.Warn("route health seed: read stable route usage", "app_id", app.ID, "err", err)
		return
	}
	usage := routeCustomerResponse(app.Slug, ids[0], requestAnalyticsWindow{From: from, Until: now, AsOf: now}, rows)
	selectors := routehealth.SeedSelectors(usage.Routes, api.RouteHealthSeedRoutes)
	if len(selectors) == 0 {
		return
	}
	expected := int64(0)
	seeded, err := store.SetRouteHealthGate(ctx, acct.ID, app.ID, api.SetRouteHealthGateRequest{Mode: "report", ExpectedRevision: &expected, Routes: selectors})
	if err != nil {
		if !errors.Is(err, state.ErrRouteHealthRevision) {
			s.log.Warn("route health seed: save report-mode selectors", "app_id", app.ID, "err", err)
		}
		return
	}
	s.audit.Emit(ctx, "route_health.seeded", &acct.ID, map[string]any{
		"app_id": app.ID, "deployment_id": candidate.ID, "stable_deployment_id": ids[0], "actor": routeHealthSeedActor,
		"mode": seeded.Mode, "revision": seeded.Revision, "route_count": len(seeded.Routes),
	})
}
