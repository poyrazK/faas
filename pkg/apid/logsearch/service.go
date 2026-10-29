// Package logsearch is apid's customer log search over the ADR-213 ledger
// (ADR-20261018083536): one filtered, newest-first page of an app's log
// events, and facet counts over the same filters. It lives outside apidserver
// per ADR-982; apidserver only wires the routes and implements Host.
package logsearch

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what log search needs from apid.
type Host interface {
	// LoadApp resolves the account's app by slug and writes the problem
	// itself when it cannot.
	LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool)
	LogEvents() state.LogEventStore
	SavedSearches() state.LogSavedSearchStore
	Logger() *slog.Logger
}

// Service serves the search. Enabled is the FAAS_LOG_SEARCH_ENABLED operator
// gate; while it is off both routes answer 503.
type Service struct {
	Host    Host
	Enabled bool
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Search serves GET /v1/apps/{slug}/log-events.
func (s *Service) Search(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, filter, ok := s.prepare(w, r, acct, false)
	if !ok {
		return
	}
	events, hasMore, err := s.Host.LogEvents().ListLogEvents(r.Context(), filter)
	if err != nil {
		s.writeReadErr(w, r.Context(), app, err)
		return
	}
	resp := api.LogEventsResponse{Since: filter.Since, Until: filter.Until, Events: make([]api.LogQueryEvent, 0, len(events))}
	for _, event := range events {
		resp.Events = append(resp.Events, QueryEvent(app.Slug, event))
	}
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		resp.NextCursor = EncodeCursor(last.OccurredAt, last.ID)
	}
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

// Facets serves GET /v1/apps/{slug}/log-events/facets.
func (s *Service) Facets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, filter, ok := s.prepare(w, r, acct, true)
	if !ok {
		return
	}
	facets, err := s.Host.LogEvents().FacetLogEvents(r.Context(), filter, api.LogSearchFacetRoutes)
	if err != nil {
		s.writeReadErr(w, r.Context(), app, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, api.LogEventFacetsResponse{
		Since: filter.Since, Until: filter.Until, Total: facets.Total,
		Sources: facetCounts(facets.Sources), Levels: facetCounts(facets.Levels),
		StatusClasses: facetCounts(facets.StatusClasses), Routes: facetCounts(facets.Routes),
		RoutesTruncated: facets.RoutesTruncated,
	})
}

func (s *Service) prepare(w http.ResponseWriter, r *http.Request, acct state.Account, facets bool) (state.App, state.LogEventFilter, bool) {
	if !s.Enabled {
		api.WriteProblem(w, api.ErrLogSearchUnavailable())
		return state.App{}, state.LogEventFilter{}, false
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, state.LogEventFilter{}, false
	}
	query, ok := s.withSaved(w, r, acct, app)
	if !ok {
		return state.App{}, state.LogEventFilter{}, false
	}
	filter, prob := ParseFilter(query, s.now(), retention(acct.Plan), facets)
	if prob != nil {
		api.WriteProblem(w, prob)
		return state.App{}, state.LogEventFilter{}, false
	}
	filter.AccountID, filter.AppID = acct.ID, app.ID
	return app, filter, true
}

func (s *Service) writeReadErr(w http.ResponseWriter, ctx context.Context, app state.App, err error) {
	if errors.Is(err, state.ErrLogSearchTimeout) {
		api.WriteProblem(w, api.ErrLogSearchTimeout())
		return
	}
	if errors.Is(err, state.ErrLogReadBusy) {
		w.Header().Set("Retry-After", "2")
		api.WriteProblem(w, api.ErrLogReadBusy())
		return
	}
	s.Host.Logger().WarnContext(ctx, "log search failed", "app_id", app.ID, "err", err)
	api.WriteProblem(w, api.ErrCapacity("could not search logs"))
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// retention is how far back the plan's ledger reaches (ADR-211).
func retention(plan api.Plan) time.Duration {
	days := 1
	if limits, ok := api.LimitsFor(plan); ok && limits.LogArchiveRetentionDaysMax > 0 {
		days = limits.LogArchiveRetentionDaysMax
	}
	return time.Duration(days) * 24 * time.Hour
}

// ParseFilter turns query parameters into a validated ledger filter. Since
// is clamped to the plan's retention; text search and facets enforce their
// window bounds. The CLI dry run and the dashboard share it.
func ParseFilter(q map[string][]string, now time.Time, keep time.Duration, facets bool) (state.LogEventFilter, *api.Problem) {
	get := func(key string) string {
		if v := q[key]; len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
		return ""
	}
	since, until, prob := window(get("since"), get("until"), now)
	if prob != nil {
		return state.LogEventFilter{}, prob
	}
	if earliest := now.Add(-keep); since.Before(earliest) {
		since = earliest
		if !since.Before(until) {
			return state.LogEventFilter{}, invalid("the window ends before the plan's log retention begins")
		}
	}
	f, err := state.ParseLogEventQuery(get)
	if err != nil {
		return state.LogEventFilter{}, invalid(err.Error())
	}
	f.Since, f.Until, f.Timeout = since, until, api.LogSearchStatementTimeout
	if span := until.Sub(since); f.Query != "" && span > api.LogSearchTextMaxWindow {
		return state.LogEventFilter{}, api.ErrLogSearchWindow("text searches", api.LogSearchTextMaxWindow, span)
	}
	if facets {
		if span := until.Sub(since); span > api.LogSearchFacetMaxWindow {
			return state.LogEventFilter{}, api.ErrLogSearchWindow("facet counts", api.LogSearchFacetMaxWindow, span)
		}
		return f, nil
	}
	f.Limit = 100
	if raw := get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.LogSearchPageMax {
			return state.LogEventFilter{}, invalid(fmt.Sprintf("limit must be 1..%d", api.LogSearchPageMax))
		}
		f.Limit = n
	}
	if raw := get("cursor"); raw != "" {
		at, id, err := DecodeCursor(raw)
		if err != nil {
			return state.LogEventFilter{}, invalid("cursor is not a next_cursor from this endpoint")
		}
		f.BeforeAt, f.BeforeID = at, id
	}
	return f, nil
}

// window parses since (a duration such as 15m, 6h, 3d, or RFC 3339) and
// until (RFC 3339, clamped to now). Since defaults to the default window.
func window(rawSince, rawUntil string, now time.Time) (time.Time, time.Time, *api.Problem) {
	until := now
	if rawUntil != "" {
		t, err := time.Parse(time.RFC3339, rawUntil)
		if err != nil {
			return time.Time{}, time.Time{}, invalid("until must be RFC 3339 (e.g. 2026-10-18T12:00:00Z)")
		}
		if t.Before(now) {
			until = t.UTC()
		}
	}
	since := until.Add(-api.LogSearchDefaultWindow)
	if rawSince != "" {
		if d, ok := lookback(rawSince); ok {
			since = until.Add(-d)
		} else if t, err := time.Parse(time.RFC3339, rawSince); err == nil {
			since = t.UTC()
		} else {
			return time.Time{}, time.Time{}, invalid("since must be a duration such as 15m, 6h or 3d, or RFC 3339")
		}
	}
	if !since.Before(until) {
		return time.Time{}, time.Time{}, invalid("since must be before until")
	}
	return since, until, nil
}

// lookback parses a positive Go duration or a whole number of days ("3d").
func lookback(raw string) (time.Duration, bool) {
	if days, ok := strings.CutSuffix(raw, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 {
			return 0, false
		}
		return time.Duration(n) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

func invalid(detail string) *api.Problem {
	return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid log search", detail)
}

// EncodeCursor makes the opaque next_cursor for the last row of a page.
func EncodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id))
}

// DecodeCursor reverses EncodeCursor.
func DecodeCursor(raw string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, "", err
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, "", errors.New("cursor has no separator")
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}, "", err
	}
	if _, err := uuid.Parse(id); err != nil {
		return time.Time{}, "", err
	}
	return at, id, nil
}

// QueryEvent is the customer shape of one ledger row.
func QueryEvent(app string, event state.LogEvent) api.LogQueryEvent {
	latencyMS := 0
	if event.LatencyMS != nil {
		latencyMS = *event.LatencyMS
	}
	return api.LogQueryEvent{
		ID: event.ID, App: app, Timestamp: event.OccurredAt.UTC().Format(time.RFC3339Nano),
		Source: api.LogSource(event.Source), DeploymentID: event.DeploymentID, InstanceID: event.InstanceID,
		RequestID: event.RequestID, TraceID: event.TraceID, Route: event.Route, Method: event.Method,
		Status: event.Status, Level: event.Level, Stream: event.Stream, Message: event.Message,
		LatencyMS: latencyMS, Count: event.Occurrences, ColdBoot: event.ColdBoot,
	}
}

func facetCounts(in []state.LogEventFacetCount) []api.LogFacetCount {
	out := make([]api.LogFacetCount, 0, len(in))
	for _, c := range in {
		out = append(out, api.LogFacetCount{Value: c.Value, Count: c.Count})
	}
	return out
}
