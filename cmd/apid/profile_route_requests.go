package main

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Request normalization is optional context, scoped to each labeled route.
func (s *server) attachProfileRouteRequests(ctx context.Context, acct state.Account, app state.App, out *api.ProfileResponse) {
	if len(out.Routes) == 0 {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	reader, ok := s.store.(state.ProfileRequestMixReader)
	if !ok || !limits.DebugTelemetryEnabled || out.Query.Start.Before(time.Now().Add(-time.Duration(limits.DebugTelemetryRetentionDays)*24*time.Hour)) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	mix, err := reader.ProfileRequestMix(ctx, acct.ID, app.ID, out.Query)
	if err != nil || mix.Total == 0 {
		return
	}
	counts := map[string]int64{}
	for _, group := range mix.Routes {
		counts[group.Label] += group.Requests
	}
	observed := map[string]bool{}
	complete := !mix.Truncated
	for i := range out.Routes {
		row := &out.Routes[i]
		if row.Route == api.ProfileUnattributedRoute {
			complete = false
			continue
		}
		count, known := counts[row.Route]
		if !known && mix.Truncated {
			continue
		}
		row.Requests = &count
		observed[row.Route] = true
		if count > 0 {
			value := row.CPUSeconds / float64(count)
			row.CPUSecondsPerRequest = &value
		}
	}
	for route := range counts {
		if !observed[route] {
			complete = false
		}
	}
	out.RouteRequestsComplete = complete
}

// Critical routes outside the bounded request-mix summary get an exact scoped
// count. All optional reads share a short deadline and cannot extend a rollout.
func (s *server) attachSelectedRouteRequests(ctx context.Context, acct state.Account, app state.App, routes []string, profiles ...*api.ProfileResponse) {
	reader, ok := s.store.(state.ProfileRequestCountReader)
	limits := api.MustLimitsFor(acct.Plan)
	if !ok || !limits.DebugTelemetryEnabled || len(routes) == 0 {
		return
	}
	selected := map[string]bool{}
	for _, route := range routes {
		selected[route] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cutoff := time.Now().Add(-time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour)
	for _, p := range profiles {
		if p.Query.Start.Before(cutoff) {
			continue
		}
		for i := range p.Routes {
			row := &p.Routes[i]
			if !selected[row.Route] || row.Requests != nil {
				continue
			}
			q := p.Query
			q.Route = row.Route
			count, available, err := reader.ProfileRequestCount(ctx, acct.ID, app.ID, q)
			if err != nil || !available || count <= 0 {
				continue
			}
			row.Requests = &count
			cost := row.CPUSeconds / float64(count)
			row.CPUSecondsPerRequest = &cost
		}
	}
}
