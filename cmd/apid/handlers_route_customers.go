package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type routeCustomerReader interface {
	RequestTelemetryRouteCustomers(context.Context, sqlc.RequestTelemetryRouteCustomersParams) ([]sqlc.RequestTelemetryRouteCustomersRow, error)
}

func (s *server) getAppRouteCustomerUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("analytics", acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id, err := uuid.Parse(r.URL.Query().Get("deployment_id"))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("deployment_id must be a deployment UUID"))
		return
	}
	deployment, err := s.store.DeploymentByID(r.Context(), r.URL.Query().Get("deployment_id"))
	if err != nil || deployment.AppID != app.ID {
		s.notFound(w, "no such deployment")
		return
	}
	window, err := routeCustomerWindow(r, time.Now().UTC(), time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	reader, ok := s.store.(routeCustomerReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("route customer analytics"))
		return
	}
	rows, err := reader.RequestTelemetryRouteCustomers(r.Context(), sqlc.RequestTelemetryRouteCustomersParams{
		AppID: stringToPgUUID(app.ID), AccountID: stringToPgUUID(acct.ID), DeploymentID: stringToPgUUID(deployment.ID),
		SinceAt: pgtype.Timestamptz{Time: window.From, Valid: true}, UntilAt: pgtype.Timestamptz{Time: window.Until, Valid: true},
		RouteLimit: api.RouteCustomerUsageMaxRoutes, CustomerLimit: api.RouteCustomerUsageMaxCustomers,
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("route customer analytics"))
		return
	}
	writeJSON(w, http.StatusOK, routeCustomerResponse(app.Slug, deployment.ID, window, rows))
}

func routeCustomerWindow(r *http.Request, now time.Time, retention time.Duration) (requestAnalyticsWindow, error) {
	window, err := parseRequestAnalyticsWindow(r, now, retention)
	if err != nil {
		return window, err
	}
	// Retention is anchored to collection time, including for historical until.
	minimum := now.Add(-retention)
	if !window.Until.After(minimum) {
		return window, fmt.Errorf("until must be within retained telemetry")
	}
	if window.From.Before(minimum) {
		window.From, window.WindowClamped = minimum, true
	}
	return window, nil
}

func routeCustomerResponse(slug, deployment string, window requestAnalyticsWindow, rows []sqlc.RequestTelemetryRouteCustomersRow) api.RouteCustomerUsageResponse {
	out := api.RouteCustomerUsageResponse{
		Slug: slug, DeploymentID: deployment, From: window.From.Format(time.RFC3339Nano), Until: window.Until.Format(time.RFC3339Nano),
		AsOf: window.AsOf.Format(time.RFC3339Nano), WindowClamped: window.WindowClamped, Coverage: "observed_only",
		Routes: []api.RouteCustomerUsage{}, RoutesLimit: api.RouteCustomerUsageMaxRoutes, CustomersLimit: api.RouteCustomerUsageMaxCustomers,
	}
	indices := map[requestAnalyticsRouteKey]int{}
	for _, row := range rows {
		key := requestAnalyticsRouteKey{route: row.Route, method: row.Method}
		index, exists := indices[key]
		if !exists {
			index = len(out.Routes)
			indices[key] = index
			out.Routes = append(out.Routes, api.RouteCustomerUsage{
				Route: row.Route, Method: row.Method, Requests: row.Requests, IdentifiedRequests: row.IdentifiedRequests,
				AnonymousRequests: row.AnonymousRequests, UnresolvedIdentityRequests: row.UnresolvedIdentityRequests,
				ConsumerCount: row.ConsumerCount, PlatformTenantCount: row.PlatformTenantCount,
				LastObservedAt: routeCustomerTimestamp(row.LastObservedAt), Customers: []api.RouteCustomerObservation{},
				CustomersTruncated: row.CustomerGroups > api.RouteCustomerUsageMaxCustomers, OtherCustomerRequests: row.OtherCustomerRequests,
			})
			out.RoutesTruncated = out.RoutesTruncated || row.MatchedRoutes > api.RouteCustomerUsageMaxRoutes
		}
		if row.ConsumerID != "" || row.PlatformTenantID != "" {
			out.Routes[index].Customers = append(out.Routes[index].Customers, api.RouteCustomerObservation{
				ConsumerID: row.ConsumerID, PlatformTenantID: row.PlatformTenantID, Requests: row.CustomerRequests,
				LastObservedAt: routeCustomerTimestamp(row.CustomerLastObservedAt),
			})
		}
	}
	return out
}

func routeCustomerTimestamp(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}
