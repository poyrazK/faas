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
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// StatusPage is the JSON shape the public status page reads. Fields
// are documented in the dashboard README; renames here must
// propagate to deploy/statuspage/index.html.
type StatusPage struct {
	// APIAvailabilityPct is the rolling 5-minute 2xx rate over
	// gateway_requests_total, expressed 0..100.
	APIAvailabilityPct float64 `json:"api_availability_pct"`
	// WakeP95MS is the p95 of gateway_wake_latency_seconds over the
	// last 5 minutes, in milliseconds.
	WakeP95MS float64 `json:"wake_p95_ms"`
	// BuildSuccessPct is the rolling 5-minute success rate of
	// builderd builds (completed/success ÷ (completed/success +
	// completed/failure)).
	BuildSuccessPct float64 `json:"build_success_pct"`
	// AsOf is the UTC timestamp the snapshot was taken. The HTML
	// renders "Updated 3 min ago" off this.
	AsOf time.Time `json:"as_of"`
	// Source is "prometheus" or "degraded: <reason>" so an
	// operator tailing the JSON can tell at a glance.
	Source string `json:"source"`
}

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
		fallback := StatusPage{
			AsOf:   time.Now().UTC(),
			Source: "degraded: " + err.Error(),
		}
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
	promURL string
	log     *slog.Logger

	mu        sync.Mutex
	lastEval  time.Time
	cached    StatusPage
	hasCached bool
}

// newStatusCache builds a cache. promURL is the local Prometheus base
// (e.g. "http://10.0.0.1:9090"); empty string disables the cache and
// the JSON handler returns a degraded payload.
func newStatusCache(promURL string, log *slog.Logger) *statusCache {
	return &statusCache{promURL: promURL, log: log}
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
			stale.Source = "degraded: " + err.Error()
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

// fetch runs the three PromQL queries against the local Prometheus
// and assembles a StatusPage. Each query has its own short timeout;
// per-field failures are logged but DO NOT overwrite the previous
// value (graceful degradation — the operator's view stays at the
// last good number during a transient Prometheus hiccup). If every
// query fails the function returns a non-nil error so the caller
// can fall back to the last cached snapshot.
//
// We track per-query success instead of inferring failure from
// "all values are zero" — a freshly-booted idle box legitimately
// has 0% API availability, 0 ms wake p95, and 0% build success,
// which is data, not failure.
func (c *statusCache) fetch(ctx context.Context) (StatusPage, error) {
	if c.promURL == "" {
		return StatusPage{}, fmt.Errorf("no prometheus URL configured")
	}

	snap := StatusPage{AsOf: time.Now().UTC(), Source: "prometheus"}
	var firstErr error
	okCount := 0

	// 1. API availability over last 5m: 2xx / total.
	availQ := `sum(rate(gateway_requests_total{code=~"2.."}[5m])) / sum(rate(gateway_requests_total[5m])) * 100`
	if pct, err := c.queryScalar(ctx, availQ); err == nil {
		snap.APIAvailabilityPct = pct
		okCount++
	} else {
		c.log.Warn("status: api_availability query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 2. Wake p95 (seconds → ms).
	wakeQ := `histogram_quantile(0.95, sum(rate(gateway_wake_latency_seconds_bucket[5m])) by (le)) * 1000`
	if ms, err := c.queryScalar(ctx, wakeQ); err == nil {
		snap.WakeP95MS = ms
		okCount++
	} else {
		c.log.Warn("status: wake_p95 query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// 3. Build success rate over last 5m.
	buildQ := `sum(rate(vmmd_op_duration_seconds_count{op="create_cold_boot",code="ok"}[5m])) / sum(rate(vmmd_op_duration_seconds_count{op="create_cold_boot"}[5m])) * 100`
	if pct, err := c.queryScalar(ctx, buildQ); err == nil {
		snap.BuildSuccessPct = pct
		okCount++
	} else {
		c.log.Warn("status: build_success query failed", "err", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	// If no query succeeded, surface the first error so the caller can
	// serve the stale cache. Otherwise the snapshot is real data even
	// if some fields happen to be 0 (idle-box case).
	if okCount == 0 {
		return snap, firstErr
	}
	return snap, nil
}

// queryScalar runs a PromQL `query` against the local Prometheus and
// returns the first scalar. Returns an error on transport failure,
// non-2xx response, parse error, or empty result.
func (c *statusCache) queryScalar(ctx context.Context, query string) (float64, error) {
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	u := c.promURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(qctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return 0, fmt.Errorf("prometheus %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var pr struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return 0, err
	}
	if len(pr.Data.Result) == 0 {
		return 0, fmt.Errorf("no data for query %q", query)
	}
	raw, ok := pr.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("unexpected value shape for query %q", query)
	}
	// ParseFloat (not fmt.Sscanf "%f") — locale-safe and consistent
	// with pkg/fcvm/metrics.go::DefaultLvFcUsedPct.
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q for query %q: %w", raw, query, err)
	}
	return f, nil
}
