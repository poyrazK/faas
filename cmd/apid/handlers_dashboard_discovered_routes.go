package main

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/openapidiff"
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
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].FirstSeen.Equal(routes[j].FirstSeen) {
			return routes[i].RouteTemplate < routes[j].RouteTemplate
		}
		return routes[i].FirstSeen.After(routes[j].FirstSeen)
	})
	statuses := s.discoveredRoutePolicyStatuses(readCtx, log, app, routes)
	view.Routes = make([]dashboard.DiscoveredRouteItem, 0, len(routes))
	for _, route := range routes {
		method, path, ok := strings.Cut(route.RouteTemplate, " ")
		if !ok {
			path = route.RouteTemplate
		}
		status := statuses[route.RouteTemplate]
		view.Routes = append(view.Routes, dashboard.DiscoveredRouteItem{
			Method:       method,
			Path:         path,
			FirstSeen:    dashboardDiscoveredRouteTime(route.FirstSeen),
			LastSeen:     dashboardDiscoveredRouteTime(route.LastSeen),
			RequestCount: route.RequestCount,
			Contract:     status.contract,
			Policy:       status.policy,
		})
	}
	return view
}

type discoveredRoutePolicyStatus struct {
	contract string
	policy   string
}

func (s *server) discoveredRoutePolicyStatuses(ctx context.Context, log *slog.Logger, app state.App, routes []state.DiscoveredAPIRoute) map[string]discoveredRoutePolicyStatus {
	statuses := make(map[string]discoveredRoutePolicyStatus, len(routes))
	for _, route := range routes {
		statuses[route.RouteTemplate] = discoveredRoutePolicyStatus{contract: "Unknown", policy: "Unknown"}
	}
	if len(routes) == 0 {
		return statuses
	}

	raw, _, docErr := s.store.GetAppOpenAPIDoc(ctx, app.ID, app.AccountID)
	docAvailable := docErr == nil || errors.Is(docErr, state.ErrNotFound)
	if docErr != nil && !errors.Is(docErr, state.ErrNotFound) {
		log.Warn("dashboard renderAppDetail: read OpenAPI for discovered-route status", "account_id", app.AccountID, "app_id", app.ID, "err", docErr)
	}
	var spec *openapidiff.Spec
	if docAvailable && len(raw) > 0 {
		var err error
		spec, err = openapidiff.LoadBytes(raw)
		if err != nil {
			docAvailable = false
			log.Warn("dashboard renderAppDetail: parse OpenAPI for discovered-route status", "account_id", app.AccountID, "app_id", app.ID, "err", err)
		}
	}

	rules, rulesErr := s.store.ListEdgeRulesForApp(ctx, app.ID)
	rulesAvailable := rulesErr == nil
	if rulesErr != nil {
		log.Warn("dashboard renderAppDetail: read edge rules for discovered-route status", "account_id", app.AccountID, "app_id", app.ID, "err", rulesErr)
		rules = nil
	}
	observed := make([]openapidiff.RouteRow, 0, len(routes))
	for _, route := range routes {
		observed = append(observed, openapidiff.RouteRow{Route: route.RouteTemplate})
	}
	for _, route := range openapidiff.BuildRoutePolicyPreview(spec, observed, rules) {
		key := strings.ToUpper(route.Method) + " " + route.Path
		status := discoveredRoutePolicyStatus{}
		if docAvailable {
			status.contract = "Undeclared"
			if route.Declared {
				status.contract = "Declared"
			}
		} else {
			status.contract = "Unknown"
		}
		if rulesAvailable {
			status.policy = "Uncovered"
			if route.Covered {
				status.policy = "Covered"
			}
		} else {
			status.policy = "Unknown"
		}
		statuses[key] = status
	}
	return statuses
}

func dashboardDiscoveredRouteTime(value time.Time) string {
	if value.IsZero() {
		return "—"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}
