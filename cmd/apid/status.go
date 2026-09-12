package main

// Status page handlers (spec §12, M8 acceptance).
//
// Two routes, both unauthenticated by design:
//   GET /status         → static HTML (live metrics, uptime history, incidents)
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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// StatusPage is the JSON shape the public status page reads. Defined
// in pkg/api so the faas CLI can import it without a back-reference
// into cmd/apid; this file uses the same alias so the existing call
// sites don't need to be rewritten. Renames here must propagate to
// deploy/statuspage/index.html.
type StatusPage = api.StatusPage

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
	snap, err := s.statusCache.Get(r.Context())
	if err != nil {
		// Even on error, return 200 with the last cached snapshot so a
		// transient Prometheus hiccup doesn't make the status page 5xx.
		// We do still surface the error in `Source` so an operator can
		// tell the snapshot is degraded.
		fallback := emptyStatusPage(time.Now().UTC(), appmetrics.SourceDegradedPrefix+err.Error())
		writeJSON(w, http.StatusOK, fallback)
		return
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
	store  state.Store
	log    *slog.Logger

	mu        sync.Mutex
	lastEval  time.Time
	cached    StatusPage
	hasCached bool
}

const (
	statusAPIAvailabilityQuery = `(
		((sum(rate(gateway_requests_total{app!="-",code=~"2.."}[5m])) or vector(0)) / sum(rate(gateway_requests_total{app!="-",code=~"2..|5.."}[5m])) * 100)
		and sum(rate(gateway_requests_total{app!="-",code=~"2..|5.."}[5m])) > 0
	) or vector(100)`
	statusWakeP95Query = `(
		(histogram_quantile(0.95, sum(rate(gateway_wake_latency_seconds_bucket[5m])) by (le)) * 1000)
		and sum(rate(gateway_wake_latency_seconds_count[5m])) > 0
	) or vector(0)`
	statusBuildSuccessQuery = `(
		(sum(rate(builderd_ops_total{op="build",code=~"ok|cache_hit"}[5m])) / sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m])) * 100)
		and sum(rate(builderd_ops_total{op="build",code!="user_error"}[5m])) > 0
	) or vector(100)`
)

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
	return &statusCache{client: c, store: store, log: log}
}

// Get returns the current snapshot, refreshing if the cache is stale
// or empty.
func (c *statusCache) Get(ctx context.Context) (StatusPage, error) {
	c.mu.Lock()
	if time.Since(c.lastEval) < 30*time.Second && c.hasCached {
		snap := c.cached
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
			stale.Source = appmetrics.SourceDegradedPrefix + err.Error()
			c.mu.Unlock()
			return stale, nil
		}
		c.mu.Unlock()
		return StatusPage{}, err
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
// per-field failures are logged but DO NOT overwrite the previous
// value (graceful degradation — the operator's view stays at the
// last good number during a transient Prometheus hiccup). If every
// primary query fails the function returns a non-nil error so the
// caller can fall back to the last cached snapshot.
//
// We track per-query success instead of inferring failure from
// "all values are zero" — a freshly-booted idle box legitimately
// has 0 ms wake p95. API and build availability use 100% when their
// denominator is empty because no request or build failed.
func (c *statusCache) fetch(ctx context.Context) (StatusPage, error) {
	if c.client == nil {
		return StatusPage{}, fmt.Errorf("no prometheus URL configured")
	}

	snap := emptyStatusPage(time.Now().UTC(), appmetrics.SourcePrometheus)
	var firstErr error
	okCount := 0

	// 1. API availability over last 5m: 2xx / eligible 2xx+5xx outcomes for
	// resolved apps. Client and application 4xx responses are not platform
	// failures and must not make the public fleet status look unavailable.
	// Requests with app="-" never reached a tenant route (unknown Host and
	// direct-address probes); counting them makes Internet scans look like a
	// platform outage.
	if pct, err := c.client.QueryScalar(ctx, statusAPIAvailabilityQuery); err == nil {
		snap.APIAvailabilityPct = pct
		okCount++
	} else {
		c.log.Warn("status: api_availability query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 2. Wake p95 (seconds → ms).
	if ms, err := c.client.QueryScalar(ctx, statusWakeP95Query); err == nil {
		snap.WakeP95MS = ms
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
		snap.BuildSuccessPct = pct
		okCount++
	} else {
		c.log.Warn("status: build_success query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 4. Degraded flag: at least one customer-impacting platform warn- or
	// page-severity alert is firing on the local Prometheus. Alerts explicitly
	// labelled public_status="internal" remain actionable for operators without
	// presenting an internal cost/capacity warning as a service outage. Tenant
	// alert-preset signals remain in Prometheus and the customer alert UI, but do
	// not describe fleet health.
	// A PromQL error here is logged but treated as
	// "no firing alerts" — the flag is intentionally conservative so
	// a transient ALERTS{} hiccup doesn't poison the public snapshot.
	// The full-pipeline failure (Prometheus unreachable) still
	// surfaces via Source = "degraded: <error>" because the primary
	// three queries would have failed first.
	alertQ := `count(ALERTS{alertstate="firing",severity=~"page|warn",family!~"alert_preset_signals|alert_preset_correlation",public_status!="internal"}) > 0`
	if v, err := c.client.QueryScalar(ctx, alertQ); err == nil {
		if v > 0 {
			snap.Degraded = true
			snap.Source = "degraded: firing alerts"
		}
	} else {
		c.log.Warn("status: alert query failed (treating as not-degraded)", "err", err)
	}

	// History is deliberately best-effort. A database hiccup must not turn a
	// healthy live SLI snapshot into a public outage; the current-state
	// Prometheus queries remain the authoritative availability signal.
	c.populateHistory(ctx, &snap)

	// If no primary query succeeded, surface the first error so the
	// caller can serve the stale cache. Otherwise the snapshot is real
	// data even if some fields happen to be 0 (idle-box case).
	if okCount == 0 {
		return snap, firstErr
	}
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

// populateHistory projects the optional state read seam into the public API
// DTO. Missing days are emitted with zero counts and 100% uptime so clients
// can render a stable 30-point sparkline and still distinguish idle from bad.
func (c *statusCache) populateHistory(ctx context.Context, snap *StatusPage) {
	history, ok := c.store.(state.StatusHistoryStore)
	if !ok || history == nil {
		return
	}
	now := time.Now().UTC()
	since := time.Date(now.Year(), now.Month(), now.Day()-statusHistoryDays+1,
		0, 0, 0, 0, time.UTC)
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
			snap.Uptime30dPct = float64(successful) / float64(total) * 100
		} else {
			snap.Uptime30dPct = 100
		}
		snap.Uptime30d = make([]api.StatusUptimeBucket, 0, statusHistoryDays)
		for i := statusHistoryDays - 1; i >= 0; i-- {
			day := time.Date(now.Year(), now.Month(), now.Day()-i, 0, 0, 0, 0, time.UTC)
			bucket := byDay[day]
			pct := 100.0
			if bucket.Total > 0 {
				pct = float64(bucket.Successful) / float64(bucket.Total) * 100
			}
			snap.Uptime30d = append(snap.Uptime30d, api.StatusUptimeBucket{
				Date:       day,
				UptimePct:  pct,
				Successful: bucket.Successful,
				Total:      bucket.Total,
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
			Component: incident.Component,
			StartedAt: incident.PostedAt.UTC(),
			Severity:  incident.Severity,
			Summary:   incident.Message,
		}
		if incident.ResolvedAt != nil {
			resolved := incident.ResolvedAt.UTC()
			projected.ResolvedAt = &resolved
		}
		snap.Incidents = append(snap.Incidents, projected)
	}
}
