package main

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardDiscoveredRoutesTimeout = 3 * time.Second

// fetchDashboardDiscoveredRoutes projects the durable inventory into the app
// detail page. A storage problem degrades this panel without blocking the
// rest of the dashboard.
func (s *server) fetchDashboardDiscoveredRoutes(ctx context.Context, log *slog.Logger, acct state.Account, app state.App) dashboard.DiscoveredRoutesView {
	view := dashboard.DiscoveredRoutesView{Routes: []dashboard.DiscoveredRouteItem{}}
	store, ok := s.store.(state.APIRouteInventoryStore)
	if !ok {
		return view
	}
	readCtx, cancel := context.WithTimeout(ctx, dashboardDiscoveredRoutesTimeout)
	defer cancel()
	routes, capHit, err := store.ListDiscoveredAPIRoutes(readCtx, acct.ID, app.ID, state.DiscoveredRouteLimit)
	if err != nil {
		log.Warn("dashboard renderAppDetail: list discovered API routes", "account_id", acct.ID, "app_id", app.ID, "err", err)
		return view
	}
	view.Available = true
	view.CapHit = capHit
	view.Routes = make([]dashboard.DiscoveredRouteItem, 0, len(routes))
	for _, route := range routes {
		method, path, ok := strings.Cut(route.RouteTemplate, " ")
		if !ok {
			path = route.RouteTemplate
		}
		view.Routes = append(view.Routes, dashboard.DiscoveredRouteItem{
			Method:       method,
			Path:         path,
			FirstSeen:    dashboardDiscoveredRouteTime(route.FirstSeen),
			LastSeen:     dashboardDiscoveredRouteTime(route.LastSeen),
			RequestCount: route.RequestCount,
		})
	}
	return view
}

func dashboardDiscoveredRouteTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}
