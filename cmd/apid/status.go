package main

// Status page handlers (spec §12, M8 acceptance).
//
// Two routes, both unauthenticated by design:
//   GET /status         → static HTML (three progress bars)
//   GET /status/slo.json → JSON snapshot the HTML reads
//
// Why unauthenticated: the status page is a public surface
// (spec §12 row "public status page"); it's read by prospects
// before sign-up and by customers during an incident. There is no
// tenant data on it — only fleet-wide SLI/SLO numbers.
//
// Why apid serves it (not a separate daemon): apid is the only
// public listener on the box (spec §Component ownership). Putting
// status on its own daemon would create a second public port + a
// second TLS cert + an inter-daemon dependency. apid is also the
// only place that already has the public hostname plumbing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/publicstatus"
	"github.com/onebox-faas/faas/pkg/state"
)

// StatusPage is the JSON shape the public status page reads. Defined
// in pkg/api so the faas CLI can import it without a back-reference
// into cmd/apid; this file uses the same alias so the existing call
// sites don't need to be rewritten. Renames here must propagate to
// deploy/statuspage/index.html.
type StatusPage = api.StatusPage

const (
	statusAPIAvailabilityQuery = `(
		((sum(rate(gateway_requests_total{app!="-",code=~"2.."}[5m])) or vector(0)) / sum(rate(gateway_requests_total{app!="-",code=~"2..|5.."}[5m])) * 100)
		and sum(rate(gateway_requests_total{app!="-",code=~"2..|5.."}[5m])) > 0
	) or vector(100)`
	statusWakeP95Query = `(
		(histogram_quantile(0.95, sum(rate(gateway_wake_latency_seconds_bucket[5m])) by (le)) * 1000)
		and sum(rate(gateway_wake_latency_seconds_count[5m])) > 0
	)`
	statusBuildSuccessQuery = `(
		(sum(rate(builderd_ops_total{op="build",code=~"ok|cache_hit"}[5m])) / sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m])) * 100)
		and sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m])) > 0
	) or vector(100)`
	statusAlertQuery = `ALERTS{alertstate="firing",severity=~"page|warn",family!~"alert_preset_signals|alert_preset_correlation",public_status!="internal"}`
)

// statusHandler serves GET /status. Reads the static HTML from disk
// (path from FAAS_STATUSPAGE_PATH or /etc/faas/statuspage/index.html
// in production, deploy/statuspage/index.html in dev). On any read
// failure we return a tiny inline "status source unavailable" page
// — the page should never 5xx just because the HTML file is missing.
func (s *server) statusHandler(w http.ResponseWriter, r *http.Request) {
	path := s.statusPagePath
	if path == "" {
		path = "/etc/faas/statuspage/index.html"
	}
	body, err := os.ReadFile(path)
	if err != nil {
		// Fall back to a minimal embedded page so the route is always
		// usable in dev (where the file isn't installed). The full
		// page lives in deploy/statuspage/index.html.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte("<!doctype html><title>faas status</title>" +
			"<body><h1>faas status</h1>" +
			"<p>Status source unavailable. JSON: <a href='/status/slo.json'>/status/slo.json</a>.</body>"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(body)
}

// statusJSONHandler serves GET /status/slo.json. The cached statusPagePath
// is configured via WithStatusCache (see server.go).
func (s *server) statusJSONHandler(w http.ResponseWriter, r *http.Request) {
	if s.statusCache == nil {
		writeJSON(w, http.StatusOK, StatusPage{
			AsOf: time.Now().UTC(), Source: appmetrics.SourceDegradedPrefix + "status evaluator is not configured",
		})
		return
	}
	snap, err := s.statusCache.Get(r.Context())
	if err != nil {
		// Even on error, return 200 with the last cached snapshot so a
		// transient Prometheus hiccup doesn't make the status page 5xx.
		// We do still surface the error in `Source` so an operator can
		// tell the snapshot is degraded.
		fallback := StatusPage{
			AsOf:   time.Now().UTC(),
			Source: appmetrics.SourceDegradedPrefix + err.Error(),
		}
		writeJSON(w, http.StatusOK, fallback)
		return
	}
	if s.store != nil {
		events, listErr := s.store.ListPublicStatusEvents(r.Context(), state.StatusEventListOptions{ActiveOnly: true, Limit: 200})
		if listErr != nil {
			snap.Source = appmetrics.SourceDegradedPrefix + "status event source unavailable"
		} else {
			states := publicstatus.ApplyOverlays(publicstatus.Evaluate(nil, nil), statusEventOverlays(events))
			if publicstatus.Overall(states, true) != publicstatus.StateOperational {
				snap.Degraded = true
				snap.Source = appmetrics.SourceDegradedPrefix + "operator status event"
			}
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_ = json.NewEncoder(w).Encode(snap)
}

// statusCache is a 30s in-process cache around the Prometheus query.
// The dashboard's auto-refresh (every 30s) would otherwise hit
// Prometheus on every page load — fine at M8's tenant count, but the
// cache bounds the work and makes the JSON endpoint safe to scrape
// from external monitoring (e.g. statuspage.io).
type statusCache struct {
	client *promql.Client
	log    *slog.Logger
	store  state.Store

	mu        sync.Mutex
	lastEval  time.Time
	cached    statusEvaluation
	hasCached bool

	publicMu       sync.Mutex
	publicCached   api.PublicStatusOverview
	publicCachedAt time.Time
	hasPublic      bool
}

type statusEvaluation struct {
	legacy             StatusPage
	states             map[publicstatus.Component]publicstatus.State
	indicatorAvailable map[string]bool
	dataStatus         string
	updatedAt          time.Time
	telemetryAvailable bool
}

// newStatusCache builds a cache. promURL is the local Prometheus base
// (e.g. "http://10.0.0.1:9090"); empty string disables the cache and
// the JSON handler returns a degraded payload. The HTTP transport
// lives in pkg/promql so tests can inject an httptest.Server and
// issue #273 / ADR-042 can reuse the client for the per-app metrics
// endpoint.
func newStatusCache(promURL string, log *slog.Logger) *statusCache {
	return newStatusCacheWithStore(promURL, nil, log)
}

func newStatusCacheWithStore(promURL string, store state.Store, log *slog.Logger) *statusCache {
	var c *promql.Client
	if promURL != "" {
		c = promql.NewClient(promURL, nil)
	}
	if log == nil {
		log = slog.Default()
	}
	return &statusCache{client: c, log: log, store: store}
}

// Get returns the current snapshot, refreshing if the cache is stale
// or empty.
func (c *statusCache) Get(ctx context.Context) (StatusPage, error) {
	evaluation, err := c.getEvaluation(ctx)
	return evaluation.legacy, err
}

func (c *statusCache) getPublic() (api.PublicStatusOverview, bool) {
	c.publicMu.Lock()
	defer c.publicMu.Unlock()
	return c.publicCached, c.hasPublic
}

func (c *statusCache) getFreshPublic(now time.Time) (api.PublicStatusOverview, bool) {
	c.publicMu.Lock()
	defer c.publicMu.Unlock()
	if !c.hasPublic || now.Sub(c.publicCachedAt) >= 15*time.Second {
		return api.PublicStatusOverview{}, false
	}
	if now.Sub(c.publicCached.UpdatedAt) > 90*time.Second {
		return api.PublicStatusOverview{}, false
	}
	return c.publicCached, true
}

func (c *statusCache) setPublic(snapshot api.PublicStatusOverview, now time.Time) {
	c.publicMu.Lock()
	c.publicCached = snapshot
	c.publicCachedAt = now
	c.hasPublic = true
	c.publicMu.Unlock()
}

func (c *statusCache) invalidatePublic() {
	c.publicMu.Lock()
	c.publicCachedAt = time.Time{}
	c.publicMu.Unlock()
}

func (c *statusCache) getEvaluation(ctx context.Context) (statusEvaluation, error) {
	c.mu.Lock()
	if time.Since(c.lastEval) < 15*time.Second && c.hasCached {
		snap := c.cached
		if time.Since(snap.updatedAt) > 90*time.Second {
			snap.dataStatus = "stale"
		}
		c.mu.Unlock()
		return snap, nil
	}
	c.mu.Unlock()

	snap, err := c.fetch(ctx)
	if err != nil {
		// Surface a stale cache rather than failing the request.
		c.mu.Lock()
		if c.hasCached {
			stale := c.cached
			stale.legacy.Source = appmetrics.SourceDegradedPrefix + err.Error()
			stale.dataStatus = "stale"
			c.mu.Unlock()
			return stale, nil
		}
		c.mu.Unlock()
		return statusEvaluation{}, err
	}

	c.mu.Lock()
	c.cached = snap
	c.lastEval = time.Now()
	c.hasCached = true
	c.mu.Unlock()
	return snap, nil
}

// fetch runs the four PromQL queries against the local Prometheus
// and assembles a StatusPage. Each query has its own short timeout;
// per-field failures are logged and represented as unavailable fields. If
// every indicator and the labeled alert query fail, the function returns an
// error so the caller can fall back to the last cached snapshot.
//
// We track per-query success instead of inferring failure from
// "all values are zero". A period with no wake observations has no p95,
// rather than a synthetic 0 ms value. API and build availability use 100% when their
// denominator is empty because no request or build failed.
func (c *statusCache) fetch(ctx context.Context) (statusEvaluation, error) {
	if c.client == nil {
		return statusEvaluation{}, fmt.Errorf("no prometheus URL configured")
	}

	now := time.Now().UTC()
	snap := statusEvaluation{
		legacy:             emptyStatusPage(now, appmetrics.SourcePrometheus),
		states:             publicstatus.Evaluate(nil, nil),
		indicatorAvailable: map[string]bool{}, dataStatus: "stale", updatedAt: now,
	}
	var firstErr error
	okCount := 0

	// 1. API availability over last 5m: 2xx / eligible 2xx+5xx outcomes
	// for resolved apps. Client/application 4xx responses and requests that
	// never reached a tenant route are not platform failures.
	if pct, err := c.client.QueryScalar(ctx, statusAPIAvailabilityQuery); err == nil {
		snap.legacy.APIAvailabilityPct = pct
		snap.indicatorAvailable["api_availability"] = true
		okCount++
	} else {
		c.log.Warn("status: api_availability query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 2. Wake p95 (seconds → ms).
	if ms, err := c.client.QueryScalar(ctx, statusWakeP95Query); err == nil {
		snap.legacy.WakeP95MS = &ms
		snap.indicatorAvailable["wake_p95"] = true
		okCount++
	} else {
		c.log.Warn("status: wake_p95 query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 3. Build success rate over last 5m. Spec §12 defines success as
	// non-user_error: an app that fails to build because of the customer's
	// own code is not a platform failure. Sourced from builderd's real
	// build counter (ADR-030) — NOT the old vmmd cold-boot proxy, which
	// measured a different thing entirely (wake success, not build).
	if pct, err := c.client.QueryScalar(ctx, statusBuildSuccessQuery); err == nil {
		snap.legacy.BuildSuccessPct = pct
		snap.indicatorAvailable["build_success"] = true
		okCount++
	} else {
		c.log.Warn("status: build_success query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 4. Degraded flag: at least one customer-impacting platform warn- or
	// page-severity alert is firing. Operator-internal and per-tenant alert
	// families remain actionable without becoming public incidents. A query
	// error here is logged but treated as
	// "no firing alerts" — the flag is intentionally conservative so
	// a transient ALERTS{} hiccup doesn't poison the public snapshot.
	// The full-pipeline failure (Prometheus unreachable) still
	// surfaces via Source = "degraded: <error>" because the primary
	// three queries would have failed first.
	if samples, err := c.client.QueryVector(ctx, statusAlertQuery); err == nil {
		alerts := make([]publicstatus.Alert, 0, len(samples))
		for _, sample := range samples {
			alerts = append(alerts, publicstatus.Alert{Severity: sample.Labels["severity"], Labels: sample.Labels})
		}
		snap.states = publicstatus.Evaluate(alerts, nil)
		snap.telemetryAvailable = true
		if len(samples) > 0 {
			snap.legacy.Degraded = true
			snap.legacy.Source = "degraded: firing alerts"
		}
	} else {
		c.log.Warn("status: labeled alert query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	if okCount == 3 && snap.telemetryAvailable {
		snap.dataStatus = "fresh"
	} else if snap.legacy.Source == appmetrics.SourcePrometheus {
		snap.legacy.Source = appmetrics.SourceDegradedPrefix + "partial telemetry"
	}

	// Fail only when both the indicators and the labeled alert source are
	// unavailable. A successful empty ALERTS vector is still authoritative
	// component telemetry; the missing indicators remain null in /v1/status.
	if okCount == 0 && !snap.telemetryAvailable {
		return snap, firstErr
	}
	c.populateHistory(ctx, &snap.legacy)
	return snap, nil
}

const statusHistoryDays = 30

func emptyStatusPage(asOf time.Time, source string) StatusPage {
	return StatusPage{
		AsOf:      asOf,
		Source:    source,
		Uptime30d: make([]api.StatusUptimeBucket, 0, statusHistoryDays),
		Incidents: make([]api.StatusIncident, 0),
	}
}

// populateHistory preserves the legacy status endpoint's best-effort history.
// Database failures do not turn healthy live telemetry into an outage.
func (c *statusCache) populateHistory(ctx context.Context, snap *StatusPage) {
	history, ok := c.store.(state.StatusHistoryStore)
	if !ok || history == nil {
		return
	}
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day()-statusHistoryDays+1, 0, 0, 0, 0, time.UTC)
	historyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	buckets, err := history.StatusUptimeBuckets(historyCtx, since)
	if err != nil {
		c.log.Warn("status: uptime history query failed", "err", err)
	} else {
		byDay := make(map[time.Time]state.StatusUptimeBucket, len(buckets))
		var successful, total int64
		for _, bucket := range buckets {
			day := bucket.Day.UTC()
			byDay[day] = bucket
			successful += bucket.Successful
			total += bucket.Total
		}
		if total > 0 {
			pct := float64(successful) / float64(total) * 100
			snap.Uptime30dPct = &pct
		}
		snap.Uptime30d = make([]api.StatusUptimeBucket, 0, statusHistoryDays)
		for i := statusHistoryDays - 1; i >= 0; i-- {
			day := time.Date(now.Year(), now.Month(), now.Day()-i, 0, 0, 0, 0, time.UTC)
			bucket := byDay[day]
			var pct *float64
			if bucket.Total > 0 {
				value := float64(bucket.Successful) / float64(bucket.Total) * 100
				pct = &value
			}
			snap.Uptime30d = append(snap.Uptime30d, api.StatusUptimeBucket{
				Date: day, UptimePct: pct, Successful: bucket.Successful, Total: bucket.Total,
			})
		}
	}

	incidents, err := history.ListStatusIncidentsSince(historyCtx, since, 100)
	if err != nil {
		c.log.Warn("status: incident history query failed", "err", err)
		return
	}
	snap.Incidents = make([]api.StatusIncident, 0, len(incidents))
	for _, incident := range incidents {
		projected := api.StatusIncident{
			Component: incident.Component, StartedAt: incident.PostedAt.UTC(),
			Severity: incident.Severity, Summary: incident.Message,
		}
		if incident.ResolvedAt != nil {
			resolved := incident.ResolvedAt.UTC()
			projected.ResolvedAt = &resolved
		}
		snap.Incidents = append(snap.Incidents, projected)
	}
}

var publicStatusComponentNames = map[publicstatus.Component]string{
	publicstatus.ComponentAPIConsole:    "API & Console",
	publicstatus.ComponentDeployments:   "Deployments",
	publicstatus.ComponentAppExecution:  "App execution",
	publicstatus.ComponentNetworking:    "Networking",
	publicstatus.ComponentObservability: "Observability",
}

func (s *server) publicStatusOverviewHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=15, stale-while-revalidate=45")
	now := time.Now().UTC()
	if s.statusCache != nil {
		if cached, ok := s.statusCache.getFreshPublic(now); ok {
			writeJSON(w, http.StatusOK, cached)
			return
		}
	}
	evaluation := unavailableStatusEvaluation()
	if s.statusCache != nil {
		if current, err := s.statusCache.getEvaluation(r.Context()); err == nil {
			evaluation = current
		}
	}
	active, upcoming, resolved := []state.StatusIncident{}, []state.StatusIncident{}, []state.StatusIncident{}
	eventsAvailable := true
	if s.store != nil {
		events, err := s.store.ListPublicStatusEvents(r.Context(), state.StatusEventListOptions{Limit: 200})
		if err != nil {
			eventsAvailable = false
			s.log.Warn("status: event history unavailable", "err", err)
			if s.statusCache != nil {
				if cached, ok := s.statusCache.getPublic(); ok {
					cached.DataStatus = "stale"
					writeJSON(w, http.StatusOK, cached)
					return
				}
			}
		}
		for _, event := range events {
			switch {
			case event.Kind == publicstatus.KindIncident && event.ResolvedAt == nil:
				active = append(active, event)
			case event.Kind == publicstatus.KindMaintenance && event.State == publicstatus.LifecycleInProgress:
				active = append(active, event)
			case event.Kind == publicstatus.KindMaintenance && event.State == publicstatus.LifecycleScheduled && event.ScheduledStartAt != nil && !event.ScheduledStartAt.Before(now) && event.ScheduledStartAt.Before(now.Add(30*24*time.Hour)):
				upcoming = append(upcoming, event)
			case event.Kind == publicstatus.KindIncident && event.ResolvedAt != nil && event.ResolvedAt.After(now.Add(-90*24*time.Hour)) && len(resolved) < 20:
				resolved = append(resolved, event)
			}
		}
	}

	states := make(map[publicstatus.Component]publicstatus.State, 5)
	for _, component := range publicstatus.AllComponents() {
		if evaluation.telemetryAvailable && eventsAvailable {
			states[component] = evaluation.states[component]
		} else {
			states[component] = publicstatus.StateUnknown
		}
	}
	states = publicstatus.ApplyOverlays(states, statusEventOverlays(active))
	if !eventsAvailable {
		evaluation.dataStatus = "unavailable"
	}

	buckets := []state.StatusBucket{}
	startDay := utcDay(now).AddDate(0, 0, -29)
	if s.store != nil {
		var err error
		buckets, err = s.store.ListStatusBuckets(r.Context(), startDay, utcDay(now).Add(24*time.Hour))
		if err != nil {
			s.log.Warn("status: daily history unavailable", "err", err)
		}
	}
	components := make([]api.PublicStatusComponent, 0, 5)
	for _, component := range publicstatus.AllComponents() {
		dailyDomain := make([]publicstatus.DailyObservation, 0, 30)
		dailyAPI := make([]api.PublicStatusDaily, 0, 30)
		for offset := 0; offset < 30; offset++ {
			day := startDay.AddDate(0, 0, offset)
			expected := 288
			if day.Equal(utcDay(now)) {
				expected = int(now.Sub(day)/(5*time.Minute)) + 1
				if expected > 288 {
					expected = 288
				}
			}
			var selected []publicstatus.Bucket
			for _, bucket := range buckets {
				if bucket.Component == component && !bucket.BucketAt.Before(day) && bucket.BucketAt.Before(day.Add(24*time.Hour)) {
					selected = append(selected, publicstatus.Bucket{At: bucket.BucketAt, State: bucket.State, HasTelemetry: bucket.HasTelemetry})
				}
			}
			observation := publicstatus.SummarizeDay(day, selected, expected)
			dailyDomain = append(dailyDomain, observation)
			dailyAPI = append(dailyAPI, api.PublicStatusDaily{Date: day.Format("2006-01-02"), Status: string(observation.State), UptimePct: observation.UptimePct, CoveragePct: observation.CoveragePct})
		}
		uptime, coverage, available := publicstatus.ThirtyDayUptime(dailyDomain)
		var uptimePtr *float64
		if available {
			uptimePtr = &uptime
		}
		components = append(components, api.PublicStatusComponent{
			ID: string(component), Name: publicStatusComponentNames[component], Status: string(states[component]),
			Uptime30DayPct: uptimePtr, Coverage30DayPct: coverage, Daily: dailyAPI,
		})
	}

	response := api.PublicStatusOverview{
		OverallStatus: string(publicstatus.Overall(states, evaluation.telemetryAvailable && eventsAvailable)),
		DataStatus:    evaluation.dataStatus, UpdatedAt: evaluation.updatedAt, RegionScope: "single-region",
		Components: components,
		Indicators: []api.PublicStatusIndicator{
			statusIndicator("api_availability", "API availability", evaluation.legacy.APIAvailabilityPct, evaluation.indicatorAvailable["api_availability"], "%", 99.9, "gte"),
			statusIndicator("wake_p95", "Wake p95", statusMetricValue(evaluation.legacy.WakeP95MS), evaluation.indicatorAvailable["wake_p95"] && evaluation.legacy.WakeP95MS != nil, "ms", 350, "lte"),
			statusIndicator("build_success", "Build success", evaluation.legacy.BuildSuccessPct, evaluation.indicatorAvailable["build_success"], "%", 99, "gte"),
		},
		ActiveEvents: publicStatusEvents(active), UpcomingMaintenance: publicStatusEvents(upcoming), ResolvedIncidents: publicStatusEvents(resolved),
	}
	if s.statusCache != nil {
		s.statusCache.setPublic(response, now)
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) publicStatusIncidentHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=15, stale-while-revalidate=45")
	if _, err := uuid.Parse(r.PathValue("public_id")); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad event id", "expected a public UUID"))
		return
	}
	if s.store == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Status event not found", "No public event exists with that id."))
		return
	}
	event, err := s.store.StatusEventByPublicID(r.Context(), r.PathValue("public_id"))
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Status event not found", "No public event exists with that id."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("status event is temporarily unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, publicStatusEvent(event))
}

func unavailableStatusEvaluation() statusEvaluation {
	states := make(map[publicstatus.Component]publicstatus.State, 5)
	for _, component := range publicstatus.AllComponents() {
		states[component] = publicstatus.StateUnknown
	}
	now := time.Now().UTC()
	return statusEvaluation{legacy: StatusPage{AsOf: now, Source: "degraded: unavailable"}, states: states, indicatorAvailable: map[string]bool{}, dataStatus: "unavailable", updatedAt: now}
}

func statusIndicator(id, label string, value float64, available bool, unit string, target float64, comparison string) api.PublicStatusIndicator {
	var ptr *float64
	if available {
		ptr = &value
	}
	return api.PublicStatusIndicator{ID: id, Label: label, Value: ptr, Unit: unit, Target: target, Comparison: comparison}
}

func statusMetricValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func publicStatusEvents(events []state.StatusIncident) []api.PublicStatusEvent {
	out := make([]api.PublicStatusEvent, 0, len(events))
	for _, event := range events {
		out = append(out, publicStatusEvent(event))
	}
	return out
}

func publicStatusEvent(event state.StatusIncident) api.PublicStatusEvent {
	components := make([]string, len(event.Components))
	for i, component := range event.Components {
		components[i] = string(component)
	}
	updates := make([]api.PublicStatusUpdate, len(event.Updates))
	for i, update := range event.Updates {
		updates[i] = api.PublicStatusUpdate{ID: update.ID, State: string(update.State), Message: update.Message, PostedAt: update.At}
	}
	return api.PublicStatusEvent{
		ID: event.PublicID, Kind: string(event.Kind), Title: event.Title, Impact: string(event.Impact), Components: components,
		State: string(event.State), StartsAt: event.StartsAt, ScheduledStartAt: event.ScheduledStartAt,
		ScheduledEndAt: event.ScheduledEndAt, UpdatedAt: event.UpdatedAt, ResolvedAt: event.ResolvedAt, Updates: updates,
	}
}

func utcDay(value time.Time) time.Time {
	year, month, day := value.UTC().Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (s *server) runStatusEvaluator(ctx context.Context) {
	if s.statusCache == nil || s.store == nil {
		return
	}
	evaluate := func() { s.runStatusEvaluationOnce(ctx) }
	evaluate()
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			evaluate()
		}
	}
}

func (s *server) runStatusEvaluationOnce(ctx context.Context) {
	evaluation, err := s.statusCache.getEvaluation(ctx)
	if err != nil {
		s.statusMetrics.observeEvaluation("error")
		s.log.Warn("status: scheduled evaluation failed", "err", err)
		return
	}
	events, err := s.store.ListPublicStatusEvents(ctx, state.StatusEventListOptions{ActiveOnly: true, Limit: 200})
	if err != nil {
		s.statusMetrics.observeEvaluation("error")
		s.log.Warn("status: active event evaluation failed", "err", err)
		return
	}
	evaluation.states = publicstatus.ApplyOverlays(evaluation.states, statusEventOverlays(events))
	s.statusMetrics.observeEvaluation(evaluation.dataStatus)
	bucketAt := time.Now().UTC().Truncate(5 * time.Minute)
	// Component state is sourced from the labeled alert query. Indicator
	// failures must not create gaps in otherwise authoritative component
	// history, especially during normal idle intervals.
	if !evaluation.telemetryAvailable {
		return
	}
	rollupSucceeded := true
	for _, component := range publicstatus.AllComponents() {
		stateValue := evaluation.states[component]
		if err := s.store.RecordStatusBucket(ctx, state.StatusBucket{Component: component, BucketAt: bucketAt, State: stateValue, HasTelemetry: true}); err != nil {
			rollupSucceeded = false
			s.log.Warn("status: record rollup bucket failed", "component", component, "err", err)
		}
	}
	if rollupSucceeded {
		s.statusMetrics.markRollupSuccess(time.Now())
	}
}

func statusEventOverlays(events []state.StatusIncident) []publicstatus.Overlay {
	overlays := make([]publicstatus.Overlay, 0, len(events))
	for _, event := range events {
		activeIncident := event.Kind == publicstatus.KindIncident && event.ResolvedAt == nil
		activeMaintenance := event.Kind == publicstatus.KindMaintenance && event.State == publicstatus.LifecycleInProgress
		if activeIncident || activeMaintenance {
			overlays = append(overlays, publicstatus.Overlay{State: event.Impact, Components: event.Components})
		}
	}
	return overlays
}
