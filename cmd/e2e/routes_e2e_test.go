// routes_e2e_test.go — KVM-free acceptance for the routes product.
//
// Customer traffic enters gatewayd-public, crosses gatewayd-internal and the
// real vmmd bridge protocol (a fake vmmd answers per path), and is recorded as
// per-route metrics and request telemetry. apid then serves the route list,
// route advice and the awake report from that telemetry; apid's own insights
// evaluator records a cacheable insight whose fix makes the gateway answer
// from its cache; and apid's production route monitor opens an incident from
// sustained 5xx traffic. When a prometheus binary is available the route list
// is also read through a real Prometheus scraping gatewayd-internal, which is
// the production path (ADR-20261024154324).
//
// Deployments and billed running time (usage_minutes) are seeded at the state
// boundary, as in the normal-path tests: the build pipeline and meterd are
// owned by other suites.

//go:build !no_pg

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

const routesCatalogBody = `{"items":["alpha","beta","gamma"]}`

type routesFixture struct {
	*normalPathFixture
	instance       state.Instance
	apidMetricsURL string
}

func newRoutesFixture(t *testing.T, slug string) *routesFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "faas-e2e-routes-telemetry-*")
	if err != nil {
		t.Fatalf("create request telemetry socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	metricsAddr := freeTCPAddr(t)
	f := newNormalPathFixtureWithPlanAndEnv(t, slug, api.PlanPro,
		"FAAS_APP_ERRORS_ENABLED=false",
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
		"FAAS_APID_REQUEST_TELEMETRY_SOCKET="+filepath.Join(dir, "request-telemetry.sock"),
		"FAAS_APID_METRICS_ADDR="+metricsAddr,
		// Production enables per-route series (gatewayd_route_metrics_enabled);
		// the daemon default is off.
		"FAAS_GATEWAY_ROUTE_METRICS=true",
	)
	if f == nil {
		return nil
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "routes")
	// Pro apps answer only API-key callers by default; a public API opens
	// itself, as the routes being measured here are anonymous traffic.
	if status := statusOnly(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen},
	}); status != http.StatusOK {
		t.Fatalf("open public auth: status=%d", status)
	}
	f.vmmd.SetVersion(instance.ID, "routes")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:routes\n", 30*time.Second)
	return &routesFixture{normalPathFixture: f, instance: instance, apidMetricsURL: "http://" + metricsAddr + "/metrics"}
}

// respond sets what the app returns on a path.
func (f *routesFixture) respond(path string, status int, body string, headers ...*vmmdpb.Header) {
	f.vmmd.SetResponseForPath(f.instance.ID, path, e2etest.FakeResponse{Status: status, Headers: headers, Body: []byte(body)})
}

// get sends one anonymous customer request through the public edge.
func (f *routesFixture) get(t *testing.T, path string) (http.Header, []byte, int) {
	t.Helper()
	return doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
}

func (f *routesFixture) api(t *testing.T, method, path string, body any, out any) int {
	t.Helper()
	raw, status := doReq(t, f.h, f.key, method, path, body, map[string]string{"Idempotency-Key": fmt.Sprintf("routes-e2e-%d", time.Now().UnixNano())})
	if out != nil && status < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: decode: %v body=%s", method, path, err, raw)
		}
	}
	if status >= 300 {
		t.Logf("%s %s: status=%d body=%s", method, path, status, raw)
	}
	return status
}

// telemetryRequests sums the requests request telemetry represents for one
// route: the gateway aggregates bursts into weighted rows, and the route
// column holds its "METHOD /path" label.
func (f *routesFixture) telemetryRequests(t *testing.T, method, route string) int {
	t.Helper()
	var n int
	if err := f.h.Pool.QueryRow(f.ctx, `select coalesce(sum(count), 0) from request_telemetry
		where app_id = $1::uuid and method = $2 and route = $3`, f.app.ID, method, method+" "+route).Scan(&n); err != nil {
		t.Fatalf("count request telemetry: %v", err)
	}
	return n
}

func (f *routesFixture) waitTelemetry(t *testing.T, method, route string, want int) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	n := 0
	for time.Now().Before(deadline) {
		if n = f.telemetryRequests(t, method, route); n >= want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("request telemetry for %s %s: %d requests after 60s, want %d; recorded for the app: %s", method, route, n, want, f.telemetrySummary(t))
}

// telemetrySummary lists the app's request telemetry by method and route, for
// failure messages.
func (f *routesFixture) telemetrySummary(t *testing.T) string {
	t.Helper()
	rows, err := f.h.Pool.Query(f.ctx, `select method, route, status, coalesce(sum(count), 0) from request_telemetry
		where app_id = $1::uuid group by 1, 2, 3 order by 4 desc limit 10`, f.app.ID)
	if err != nil {
		return "query failed: " + err.Error()
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var method, route string
		var status, n int
		if err := rows.Scan(&method, &route, &status, &n); err != nil {
			return "scan failed: " + err.Error()
		}
		out = append(out, fmt.Sprintf("%s %q %d x%d", method, route, status, n))
	}
	var total int
	_ = f.h.Pool.QueryRow(f.ctx, `select count(*) from request_telemetry`).Scan(&total)
	return fmt.Sprintf("%v (all apps: %d rows); apid counters: %v", out, total, metricLines(f.apidMetricsURL, "request_telemetry"))
}

// metricLines returns the non-zero samples whose name contains substr.
func metricLines(url, substr string) []string {
	resp, err := http.Get(url) //nolint:gosec,noctx // loopback test listener
	if err != nil {
		return []string{"scrape failed: " + err.Error()}
	}
	defer resp.Body.Close()
	var out []string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || !strings.Contains(line, substr) || strings.HasSuffix(line, " 0") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// seedRunningTime records billed running time for the app over the last
// minutes, as meterd does for a running instance.
func (f *routesFixture) seedRunningTime(t *testing.T, minutes int) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Minute)
	for i := range minutes {
		if _, err := f.h.Pool.Exec(f.ctx, `insert into usage_minutes (account_id, app_id, instance_id, minute, mb_seconds)
			select account_id, id, $2::uuid, $3, $4 from apps where id = $1::uuid`, f.app.ID, f.instance.ID,
			now.Add(-time.Duration(i)*time.Minute), int64(256+8)*60); err != nil {
			t.Fatalf("seed usage_minutes: %v", err)
		}
	}
}

// apidMetric reads one sample from apid's /metrics; ok is false when the
// series is absent.
func (f *routesFixture) apidMetric(t *testing.T, series string) (float64, bool) {
	t.Helper()
	return scrapeMetric(t, f.apidMetricsURL, series)
}

func scrapeMetric(t *testing.T, url, series string) (float64, bool) {
	t.Helper()
	resp, err := http.Get(url) //nolint:gosec,noctx // loopback test listener
	if err != nil {
		t.Fatalf("scrape %s: %v", url, err)
	}
	defer resp.Body.Close()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, series+" ") {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, series)), 64)
		if err != nil {
			t.Fatalf("parse %s: %v", line, err)
		}
		return v, true
	}
	return 0, false
}

func routeListed(routes []string, want string) bool { return slices.Contains(routes, want) }

// TestE2E_Routes_DiscoveryInsightsAndCacheFix drives the routes journey a
// customer sees for an app nobody configured: traffic is listed per route,
// the advisor and apid's insights evaluator find a cacheable route from the
// gateway's measurements, and applying the insight's fix makes the gateway
// serve that route from its cache without waking the app.
func TestE2E_Routes_DiscoveryInsightsAndCacheFix(t *testing.T) {
	f := newRoutesFixture(t, "routes-journey")
	if f == nil {
		return
	}
	jsonType := &vmmdpb.Header{Name: "Content-Type", Value: "application/json"}
	f.respond("/catalog", http.StatusOK, routesCatalogBody, jsonType)
	f.respond("/health", http.StatusOK, "ok")
	f.respond("/broken", http.StatusInternalServerError, "boom")

	// The cacheable evidence the advisor needs: at least
	// RouteAdviceMinRequests anonymous successes, repeated within a minute
	// with the same body.
	catalogRequests := api.RouteAdviceMinRequests + 60
	for range catalogRequests {
		headers, body, status := f.get(t, "/catalog")
		if status != http.StatusOK || string(body) != routesCatalogBody || headers.Get("x-faas-cache") == "hit" {
			t.Fatalf("GET /catalog: status=%d cache=%q body=%q", status, headers.Get("x-faas-cache"), body)
		}
	}
	for range 20 {
		if _, _, status := f.get(t, "/health"); status != http.StatusOK {
			t.Fatalf("GET /health: status=%d", status)
		}
		if _, _, status := f.get(t, "/broken"); status != http.StatusInternalServerError {
			t.Fatalf("GET /broken: status=%d", status)
		}
	}
	f.waitTelemetry(t, http.MethodGet, "/catalog", catalogRequests)
	f.waitTelemetry(t, http.MethodGet, "/broken", 20)
	f.seedRunningTime(t, 3)

	slug := f.app.Slug
	// Without Prometheus apid reads the single gateway's control listener.
	var routes api.AppRoutesResponse
	if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/routes", nil, &routes); status != http.StatusOK {
		t.Fatalf("GET routes: status=%d", status)
	}
	for _, want := range []string{"GET /catalog", "GET /health", "GET /broken"} {
		if !routeListed(routes.Routes, want) {
			t.Fatalf("routes = %+v, want %q listed", routes, want)
		}
	}
	if routes.Source != api.AppRoutesSourceLive {
		t.Fatalf("routes source = %q, want %q", routes.Source, api.AppRoutesSourceLive)
	}
	// The gateway exports these pairs within its per-route series budget.
	if pairs, ok := scrapeMetric(t, f.h.GatewayControlURL+"/metrics", "gateway_route_metrics_routes"); !ok || pairs < 3 {
		t.Fatalf("gateway_route_metrics_routes = %v (present %v), want >= 3", pairs, ok)
	}
	if budget, ok := scrapeMetric(t, f.h.GatewayControlURL+"/metrics", "gateway_route_metrics_budget_routes"); !ok || budget != api.RouteMetricsMaxRoutesPerGateway {
		t.Fatalf("gateway_route_metrics_budget_routes = %v (present %v), want %d", budget, ok, api.RouteMetricsMaxRoutesPerGateway)
	}

	var advice api.RouteAdviceResponse
	if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/routes/advice?since=1h", nil, &advice); status != http.StatusOK {
		t.Fatalf("GET route advice: status=%d", status)
	}
	if !slices.ContainsFunc(advice.Suggestions, func(s api.RouteAdviceSuggestion) bool {
		return s.Kind == api.RouteAdviceKindCache && s.Method == http.MethodGet && s.Route == "/catalog"
	}) {
		t.Fatalf("route advice = %+v, want a cache suggestion for GET /catalog", advice)
	}

	var awake api.RouteAwakeResponse
	if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/routes/awake?since=1h", nil, &awake); status != http.StatusOK {
		t.Fatalf("GET route awake: status=%d", status)
	}
	if awake.AwakeSeconds <= 0 || !slices.ContainsFunc(awake.Routes, func(r api.RouteAwakeRoute) bool {
		return r.Method == http.MethodGet && r.Route == "/catalog" && r.Requests >= int64(catalogRequests)
	}) {
		t.Fatalf("route awake = %+v, want awake time attributed to GET /catalog", awake)
	}

	// Production lists routes from the fleet Prometheus. Restarting apid also
	// restarts its insights evaluator's first-run timer, so the first run
	// sees the telemetry above.
	promURL, prometheus := startRoutesPrometheus(t, f.h.GatewayControlURL, f.nodeID)
	if prometheus {
		// apid counts a node as a route collector once it registers a gateway
		// target, as production nodes do for Prometheus HTTP-SD.
		target := "tcp://" + strings.TrimPrefix(f.h.EdgeURL(), "http://")
		if _, err := f.h.Pool.Exec(f.ctx, `update compute_nodes set gateway_target_url = $2 where id = $1::uuid`, f.nodeID, target); err != nil {
			t.Fatalf("register gateway target: %v", err)
		}
		if err := f.h.SetAPIDEnv("FAAS_PROMETHEUS_URL", promURL); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.h.RestartAPID(); err != nil {
		t.Fatalf("restart apid: %v", err)
	}
	insightsDeadline := time.Now().Add(routesInsightsFirstRun + 90*time.Second)
	if prometheus {
		waitRoutesFromPrometheus(t, f, promURL, []string{"GET /catalog", "GET /health", "GET /broken"})
	}

	insight := waitCacheableInsight(t, f, "/catalog", insightsDeadline)
	if insight.Fix == nil || insight.Fix.Action != api.RouteInsightFixCreateCacheRule {
		t.Fatalf("cacheable insight fix = %+v, want %s", insight.Fix, api.RouteInsightFixCreateCacheRule)
	}
	if runs, ok := f.apidMetric(t, `apid_route_insights_runs_total{outcome="success"}`); !ok || runs < 1 {
		t.Fatalf("apid_route_insights_runs_total{success} = %v (present %v), want >= 1", runs, ok)
	}
	if apps, ok := f.apidMetric(t, `apid_route_insights_apps_total{outcome="evaluated"}`); !ok || apps < 1 {
		t.Fatalf("apid_route_insights_apps_total{evaluated} = %v (present %v), want >= 1", apps, ok)
	}

	var fixed api.RouteInsightFixResponse
	if status := f.api(t, http.MethodPost, "/v1/apps/"+slug+"/routes/insights/"+insight.ID+"/fix",
		api.RouteInsightFixRequest{FixID: insight.Fix.ID}, &fixed); status != http.StatusOK {
		t.Fatalf("apply insight fix: status=%d", status)
	}
	if fixed.Insight.FixedAt == "" || fixed.Rule.ID == "" {
		t.Fatalf("fix response = %+v, want a fixed insight and a created rule", fixed)
	}
	waitRouteCacheHit(t, f, "/catalog")
	before := f.vmmd.ForwardCount()
	for range 5 {
		headers, body, status := f.get(t, "/catalog")
		if status != http.StatusOK || string(body) != routesCatalogBody || headers.Get("x-faas-cache") != "hit" {
			t.Fatalf("cached GET /catalog: status=%d cache=%q body=%q", status, headers.Get("x-faas-cache"), body)
		}
	}
	if after := f.vmmd.ForwardCount(); after != before {
		t.Fatalf("cache hits reached the app: forwards %d -> %d", before, after)
	}
}

// TestE2E_Routes_ProductionMonitorOpensIncident saves production route
// budgets and holds one route over its error budget; apid's monitor worker
// must confirm the sustained violation and open one saved incident.
func TestE2E_Routes_ProductionMonitorOpensIncident(t *testing.T) {
	f := newRoutesFixture(t, "routes-monitor")
	if f == nil {
		return
	}
	f.respond("/checkout", http.StatusInternalServerError, "checkout failed")
	f.respond("/health", http.StatusOK, "ok")

	slug := f.app.Slug
	budget := int64(100) // 1% 5xx
	zero := int64(0)
	var config api.RouteMonitorConfig
	if status := f.api(t, http.MethodPut, "/v1/apps/"+slug+"/route-monitor", api.SetRouteMonitorRequest{
		Enabled: true, ExpectedRevision: &zero,
		Routes: []api.RouteMonitorRoute{
			{Method: http.MethodGet, Path: "/checkout", Max5xxRateBPS: &budget},
			{Method: http.MethodGet, Path: "/health", Max5xxRateBPS: &budget},
		},
	}, &config); status != http.StatusOK {
		t.Fatalf("PUT route monitor: status=%d", status)
	}
	if !config.Enabled || config.Revision != 1 || len(config.Routes) != 2 {
		t.Fatalf("route monitor config = %+v", config)
	}

	// Two closed one-minute windows, each with at least 20 requests per
	// route, must both violate after the configuration change.
	ctx, stop := context.WithCancel(context.Background())
	var traffic sync.WaitGroup
	var served, failed atomic.Int64
	traffic.Add(1)
	go func() {
		defer traffic.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for path, want := range map[string]int{"/checkout": http.StatusInternalServerError, "/health": http.StatusOK} {
					if status, _, err := gatewayReqNoFatal(f, path); err == nil && status == want {
						served.Add(1)
					} else {
						failed.Add(1)
					}
				}
			}
		}
	}()
	defer func() { stop(); traffic.Wait() }()
	// The monitor reads request telemetry; fail in seconds, not minutes, when
	// none is being recorded.
	f.waitTelemetry(t, http.MethodGet, "/checkout", 5)

	var page api.RouteMonitorIncidentPage
	deadline := time.Now().Add(7 * time.Minute)
	for time.Now().Before(deadline) {
		if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/route-monitor/incidents", nil, &page); status != http.StatusOK {
			t.Fatalf("GET incidents: status=%d", status)
		}
		if len(page.Incidents) > 0 {
			break
		}
		time.Sleep(10 * time.Second)
	}
	if len(page.Incidents) != 1 {
		var report api.RouteMonitorReport
		f.api(t, http.MethodGet, "/v1/apps/"+slug+"/route-monitor/report", nil, &report)
		t.Fatalf("incidents = %d after 7m, want 1; traffic served=%d failed=%d; telemetry rows /checkout=%d /health=%d; latest report = %+v",
			len(page.Incidents), served.Load(), failed.Load(), f.telemetryRequests(t, http.MethodGet, "/checkout"),
			f.telemetryRequests(t, http.MethodGet, "/health"), report)
	}
	incident := page.Incidents[0]
	if incident.Status != "open" || incident.OpeningReport.Status != "violated" {
		t.Fatalf("incident = %s/%s, want open with a violated opening report", incident.Status, incident.OpeningReport.Status)
	}
	violated := slices.ContainsFunc(incident.OpeningReport.Routes, func(r api.RouteMonitorFinding) bool {
		return r.Route.Path == "/checkout" && r.ErrorStatus == "violated"
	})
	healthy := slices.ContainsFunc(incident.OpeningReport.Routes, func(r api.RouteMonitorFinding) bool {
		return r.Route.Path == "/health" && r.ErrorStatus == "healthy"
	})
	if !violated || !healthy {
		t.Fatalf("opening report routes = %+v, want /checkout violated and /health healthy", incident.OpeningReport.Routes)
	}
	var detail api.RouteMonitorIncident
	if status := f.api(t, http.MethodGet, "/v1/apps/"+slug+"/route-monitor/incidents/"+incident.ID, nil, &detail); status != http.StatusOK || detail.ID != incident.ID {
		t.Fatalf("GET incident %s: status=%d id=%q", incident.ID, status, detail.ID)
	}
	if evaluated, ok := f.apidMetric(t, `apid_route_monitor_evaluations_total{outcome="evaluated"}`); !ok || evaluated < 2 {
		t.Fatalf("apid_route_monitor_evaluations_total{evaluated} = %v (present %v), want >= 2", evaluated, ok)
	}
	if _, ok := f.apidMetric(t, "apid_route_monitor_lag_seconds"); !ok {
		t.Fatal("apid_route_monitor_lag_seconds not exported")
	}
}

// gatewayReqNoFatal sends background traffic; a failure is the caller's
// assertion to make, not a reason to stop the generator from its goroutine.
func gatewayReqNoFatal(f *routesFixture, path string) (int, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, f.h.EdgeURL()+path, nil) //nolint:noctx // bounded by the client timeout
	if err != nil {
		return 0, nil, err
	}
	req.Host = f.host
	client := *f.h.HTTPClient()
	client.Timeout = 10 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// routesInsightsFirstRun matches routeinsights' first-run delay after apid
// starts.
const routesInsightsFirstRun = 2 * time.Minute

func waitCacheableInsight(t *testing.T, f *routesFixture, route string, deadline time.Time) api.RouteInsight {
	t.Helper()
	var out api.RouteInsightsResponse
	for time.Now().Before(deadline) {
		if status := f.api(t, http.MethodGet, "/v1/apps/"+f.app.Slug+"/routes/insights", nil, &out); status != http.StatusOK {
			t.Fatalf("GET route insights: status=%d", status)
		}
		for _, insight := range out.Insights {
			if insight.Kind == "cacheable" && insight.Method == http.MethodGet && insight.Route == route && insight.State == "active" {
				return insight
			}
		}
		time.Sleep(5 * time.Second)
	}
	runs, _ := f.apidMetric(t, `apid_route_insights_runs_total{outcome="success"}`)
	failed, _ := f.apidMetric(t, `apid_route_insights_runs_total{outcome="failed"}`)
	t.Fatalf("no active cacheable insight for GET %s; insights=%+v runs success=%v failed=%v", route, out.Insights, runs, failed)
	return api.RouteInsight{}
}

func waitRouteCacheHit(t *testing.T, f *routesFixture, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	cache := ""
	for time.Now().Before(deadline) {
		headers, _, status := f.get(t, path)
		if cache = headers.Get("x-faas-cache"); status == http.StatusOK && cache == "hit" {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("GET %s never served from the edge cache after the fix (last x-faas-cache=%q)", path, cache)
}

// startRoutesPrometheus runs a real Prometheus scraping gatewayd-internal as
// the production gatewayd-internal job does. It reports false, and the test
// keeps to the single-gateway path, when no prometheus binary is available.
func startRoutesPrometheus(t *testing.T, gatewayControlURL, nodeID string) (string, bool) {
	t.Helper()
	bin := os.Getenv("FAAS_E2E_PROMETHEUS_BIN")
	if bin == "" {
		var err error
		if bin, err = exec.LookPath("prometheus"); err != nil {
			t.Log("prometheus not found; the route list is checked through the gateway control listener only")
			return "", false
		}
	}
	dir := t.TempDir()
	target := strings.TrimPrefix(gatewayControlURL, "http://")
	config := fmt.Sprintf(`global:
  scrape_interval: 1s
scrape_configs:
  - job_name: gatewayd-internal
    static_configs:
      - targets: [%q]
        labels:
          node_id: %q
`, target, nodeID)
	configPath := filepath.Join(dir, "prometheus.yml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := freeTCPAddr(t)
	cmd := exec.Command(bin, "--config.file="+configPath, "--storage.tsdb.path="+filepath.Join(dir, "data"),
		"--web.listen-address="+addr, "--log.level=warn")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start prometheus: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	url := "http://" + addr
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url + "/-/ready"); err == nil { //nolint:gosec,noctx // loopback test listener
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return url, true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("prometheus at %s not ready after 30s", url)
	return "", false
}

// waitRoutesFromPrometheus waits until apid lists the routes from the fleet
// Prometheus with its one gateway collector healthy.
func waitRoutesFromPrometheus(t *testing.T, f *routesFixture, promURL string, want []string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var routes api.AppRoutesResponse
	for time.Now().Before(deadline) {
		routes = api.AppRoutesResponse{}
		if status := f.api(t, http.MethodGet, "/v1/apps/"+f.app.Slug+"/routes", nil, &routes); status == http.StatusOK &&
			routes.Source == api.AppRoutesSourceLive && routes.CollectorsExpected == 1 && routes.CollectorsHealthy == 1 &&
			!slices.ContainsFunc(want, func(r string) bool { return !routeListed(routes.Routes, r) }) {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("routes through Prometheus = %+v, want %v from one healthy collector; prometheus targets: %s; up: %s",
		routes, want, prometheusGet(promURL, "/api/v1/targets?state=active"), prometheusGet(promURL, "/api/v1/query?query=up"))
}

// prometheusGet returns a Prometheus API response body for failure messages.
func prometheusGet(promURL, path string) string {
	resp, err := http.Get(promURL + path) //nolint:gosec,noctx // loopback test listener
	if err != nil {
		return "request failed: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return string(body)
}
