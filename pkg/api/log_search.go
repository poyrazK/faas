package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// SearchAppLogEvents runs GET /v1/apps/{slug}/log-events with the given
// query parameters (since, until, source, level, route, status,
// deployment_id, request_id, trace_id, q, limit, cursor).
func (c *Client) SearchAppLogEvents(ctx context.Context, slug string, filters url.Values) (LogEventsResponse, error) {
	var out LogEventsResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/log-events"
	if len(filters) > 0 {
		path += "?" + filters.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetAppLogEventFacets runs GET /v1/apps/{slug}/log-events/facets with the
// search filters (cursor and limit do not apply).
func (c *Client) GetAppLogEventFacets(ctx context.Context, slug string, filters url.Values) (LogEventFacetsResponse, error) {
	var out LogEventFacetsResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/log-events/facets"
	if len(filters) > 0 {
		path += "?" + filters.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}

// ErrLogSearchWindow rejects a text or facet search whose window exceeds its
// bound. Limit and observed are seconds.
func ErrLogSearchWindow(kind string, limit, observed time.Duration) *Problem {
	p := NewProblem(http.StatusBadRequest, "log_search_window_too_large", "Log search window too large",
		fmt.Sprintf("%s cover at most %s; this one covers %s", kind, limit, observed.Round(time.Second)))
	l, o := int64(limit/time.Second), int64(observed/time.Second)
	p.Limit, p.Observed = &l, &o
	return p.WithDocs(docsBase + "/log-search").WithHint("narrow --since/--until, or drop the text query")
}

// ErrLogSearchTimeout reports a search that hit its statement timeout.
func ErrLogSearchTimeout() *Problem {
	return NewProblem(http.StatusServiceUnavailable, "log_search_timeout", "Log search timed out",
		"the search did not finish in time").WithDocs(docsBase + "/log-search").
		WithHint("narrow the window or add filters such as --source, --level or --route")
}

// ErrLogSearchUnavailable is the FAAS_LOG_SEARCH_ENABLED gate's answer.
func ErrLogSearchUnavailable() *Problem {
	return NewProblem(http.StatusServiceUnavailable, "log_search_unavailable", "Log search unavailable",
		"the log search preview is not enabled")
}

// LogEventsResponse is GET /v1/apps/{slug}/log-events (ADR-20261018083536):
// one newest-first page of the app's ADR-213 ledger. Since may be later than
// requested when the request reached past the plan's log retention.
type LogEventsResponse struct {
	Since      time.Time       `json:"since"`
	Until      time.Time       `json:"until"`
	Events     []LogQueryEvent `json:"events"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// LogFacetCount is one value of a facet and how many ledger events carry it.
type LogFacetCount struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// LogEventFacetsResponse is GET /v1/apps/{slug}/log-events/facets: match
// counts by source, level, status class (2xx..5xx), and the busiest routes.
// Counts sum each event's occurrences, so a deduplicated row counts as many
// times as it happened.
type LogEventFacetsResponse struct {
	Since           time.Time       `json:"since"`
	Until           time.Time       `json:"until"`
	Total           int64           `json:"total"`
	Sources         []LogFacetCount `json:"sources"`
	Levels          []LogFacetCount `json:"levels"`
	StatusClasses   []LogFacetCount `json:"status_classes"`
	Routes          []LogFacetCount `json:"routes"`
	RoutesTruncated bool            `json:"routes_truncated"`
}

// LogSavedSearch is one app's named log search (ADR-20261018083536 slice D).
// Query holds filters only (source, level, status, route, deployment_id,
// request_id, trace_id, q) as a URL query string; each search or alert
// brings its own window.
type LogSavedSearch struct {
	Name      string    `json:"name"`
	Query     string    `json:"query"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LogSavedSearchRequest is the body of PUT /v1/apps/{slug}/log-searches/{name}.
type LogSavedSearchRequest struct {
	Query string `json:"query"`
}

// LogSavedSearchesResponse is GET /v1/apps/{slug}/log-searches.
type LogSavedSearchesResponse struct {
	Searches []LogSavedSearch `json:"searches"`
}

// ListAppLogSavedSearches runs GET /v1/apps/{slug}/log-searches.
func (c *Client) ListAppLogSavedSearches(ctx context.Context, slug string) (LogSavedSearchesResponse, error) {
	var out LogSavedSearchesResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/log-searches", nil, &out)
}

// PutAppLogSavedSearch runs PUT /v1/apps/{slug}/log-searches/{name}: it
// creates the search or replaces its query.
func (c *Client) PutAppLogSavedSearch(ctx context.Context, slug, name, query string) (LogSavedSearch, error) {
	var out LogSavedSearch
	path := "/v1/apps/" + url.PathEscape(slug) + "/log-searches/" + url.PathEscape(name)
	return out, c.do(ctx, "PUT", path, LogSavedSearchRequest{Query: query}, &out)
}

// DeleteAppLogSavedSearch runs DELETE /v1/apps/{slug}/log-searches/{name}.
func (c *Client) DeleteAppLogSavedSearch(ctx context.Context, slug, name string) error {
	path := "/v1/apps/" + url.PathEscape(slug) + "/log-searches/" + url.PathEscape(name)
	return c.do(ctx, "DELETE", path, nil, nil)
}

// ErrLogSavedSearchNotFound names a saved search the app does not have.
func ErrLogSavedSearchNotFound(name string) *Problem {
	return NewProblem(http.StatusNotFound, "log_saved_search_not_found", "Saved log search not found",
		"the app has no saved log search named "+name).WithDocs(docsBase + "/log-search").
		WithHint("list them with `gregale logs saved`")
}

// ErrLogSavedSearchQuota rejects a new saved search past the per-app cap.
func ErrLogSavedSearchQuota(limit, observed int) *Problem {
	p := NewProblem(http.StatusConflict, "log_saved_search_quota_exceeded", "Saved log search limit reached",
		fmt.Sprintf("the app has %d of %d saved log searches", observed, limit))
	l, o := int64(limit), int64(observed)
	p.Limit, p.Observed = &l, &o
	return p.WithDocs(docsBase + "/log-search").WithHint("delete a saved search with `gregale logs saved delete`")
}
