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
