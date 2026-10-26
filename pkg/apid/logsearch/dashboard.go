package logsearch

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardFields are the query parameters the page's form submits.
var dashboardFields = []string{"q", "since", "level", "status", "route", "source", "cursor"}

// DashboardData builds the log search page. Facets are shown on the first
// page when the window is within the facet bound.
func (s *Service) DashboardData(ctx context.Context, acct state.Account, app state.App, query url.Values) dashboard.AppLogSearchData {
	data := dashboard.AppLogSearchData{Enabled: s.Enabled, AppSlug: app.Slug,
		SourceOptions: []string{"runtime", "build", "deploy", "http", "network", "dns"}, Form: dashboard.LogSearchForm{
			Q: query.Get("q"), Since: query.Get("since"), Level: query.Get("level"),
			Status: query.Get("status"), Route: query.Get("route"), Source: query.Get("source"),
		}}
	if !data.Enabled {
		return data
	}
	if data.Form.Since == "" {
		data.Form.Since = "1h"
	}
	params := url.Values{}
	for _, key := range dashboardFields {
		if v := query.Get(key); v != "" {
			params.Set(key, v)
		}
	}
	params.Set("since", data.Form.Since)
	filter, prob := ParseFilter(params, s.now(), retention(acct.Plan), false)
	if prob != nil {
		data.Error = prob.Detail
		return data
	}
	filter.AccountID, filter.AppID = acct.ID, app.ID
	events, hasMore, err := s.Host.LogEvents().ListLogEvents(ctx, filter)
	if err != nil {
		data.Error = s.readErrMessage(ctx, app, err)
		return data
	}
	results := api.LogEventsResponse{Since: filter.Since, Until: filter.Until, Events: make([]api.LogQueryEvent, 0, len(events))}
	for _, event := range events {
		results.Events = append(results.Events, QueryEvent(app.Slug, event))
	}
	data.Results = &results
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		next := url.Values{}
		for key, v := range params {
			next[key] = v
		}
		next.Set("cursor", EncodeCursor(last.OccurredAt, last.ID))
		data.NextURL = "/dashboard/apps/" + url.PathEscape(app.Slug) + "/log-search?" + next.Encode()
	}
	if query.Get("cursor") == "" && filter.Until.Sub(filter.Since) <= api.LogSearchFacetMaxWindow {
		facetFilter := filter
		facetFilter.BeforeAt, facetFilter.BeforeID = time.Time{}, ""
		if facets, err := s.Host.LogEvents().FacetLogEvents(ctx, facetFilter, api.LogSearchFacetRoutes); err == nil {
			data.Facets = &api.LogEventFacetsResponse{
				Since: filter.Since, Until: filter.Until, Total: facets.Total,
				Sources: facetCounts(facets.Sources), Levels: facetCounts(facets.Levels),
				StatusClasses: facetCounts(facets.StatusClasses), Routes: facetCounts(facets.Routes),
				RoutesTruncated: facets.RoutesTruncated,
			}
		}
	}
	return data
}

// readErrMessage is the page's wording for a failed read.
func (s *Service) readErrMessage(ctx context.Context, app state.App, err error) string {
	if errors.Is(err, state.ErrLogSearchTimeout) {
		return "The search did not finish in time. Narrow the window or add a filter."
	}
	if errors.Is(err, state.ErrLogReadBusy) {
		return "Too many log searches are running for this account. Try again in a few seconds."
	}
	s.Host.Logger().WarnContext(ctx, "log search page failed", "app_id", app.ID, "err", err)
	return "Logs could not be searched right now."
}
