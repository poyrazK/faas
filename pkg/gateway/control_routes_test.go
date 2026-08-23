package gateway

// Tests for the ADR-093 bridge (ADR-126 / issue #975 item #2).
// The bridge exposes per-app per-route observation rows to the
// apid auto-gen endpoint. Tests cover:
//
//   - RouteRowsFor returns the labels-only snapshot when no
//     metric is registered yet (the dashboard's "no traffic
//     yet" state)
//   - RouteRowsFor returns __route_other__ when the app is at
//     the cap (overflowed=true) and has no admitted labels
//   - RouteRowsFor joins the routeLabelSet with the per-route
//     Prometheus counter (counts sum across status codes)
//   - RouteRowsFor computes error_pct from
//     failures/counts*100
//   - quantilesFromBuckets matches PromQL histogram_quantile
//     within float epsilon
//   - ControlMuxRouteRows registers the new endpoint and the
//     handler returns the documented envelope shape

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// makeTestHandler constructs a Handler with a real Metrics
// instance and pre-admitted routes. Tests use this to drive
// RouteRowsFor end-to-end without standing up a full gatewayd.
func makeTestHandler(t *testing.T, appID string, routes []string) *Handler {
	t.Helper()
	m := NewMetricsForTest()
	h := &Handler{metrics: m}
	// Pre-admit the test routes via the test seam.
	set := h.RouteSetForTest(appID)
	for _, r := range routes {
		set.AdmitForTest(r)
	}
	return h
}

// TestRouteRowsFor_EmptyApp pins the "no traffic yet" state.
// The Handler has a routeLabelSet for appID but no Prometheus
// series yet — RouteRowsFor returns the labels with zero counts.
func TestRouteRowsFor_EmptyApp(t *testing.T) {
	h := makeTestHandler(t, "app-1", []string{"GET /users", "GET /healthz"})
	rows := h.RouteRowsFor("app-1")
	if len(rows) != 2 {
		t.Fatalf("rows: got %d, want 2", len(rows))
	}
	want := []string{"GET /healthz", "GET /users"} // sorted
	for i, r := range rows {
		if r.Route != want[i] {
			t.Errorf("rows[%d].Route: got %q, want %q", i, r.Route, want[i])
		}
		if r.Count != 0 {
			t.Errorf("rows[%d].Count: got %d, want 0", i, r.Count)
		}
		if r.P50MS != 0 || r.P95MS != 0 || r.P99MS != 0 {
			t.Errorf("rows[%d]: percentiles should be 0 with no histogram data", i)
		}
		if r.ErrorPct != 0 {
			t.Errorf("rows[%d].ErrorPct: got %v, want 0", i, r.ErrorPct)
		}
	}
}

// TestRouteRowsFor_CapHitWithRealRoutes pins the "cap-hit AND
// real routes" state — the routeLabelSet has 50 real routes
// admitted (cap reached). RouteRowsFor returns those 50 real
// rows; the dashboard renders the "you're at the cap" warning
// separately based on the cap-hit flag from RoutesFor. We don't
// surface __route_other__ here because there ARE real routes
// to show.
func TestRouteRowsFor_CapHitWithRealRoutes(t *testing.T) {
	m := NewMetricsForTest()
	h := &Handler{metrics: m}
	// Force-create the routeLabelSet + force overflowed by
	// pre-admitting cap real labels.
	set := h.RouteSetForTest("app-overflow")
	for i := 0; i < routeLabelSetCap; i++ {
		set.AdmitForTest("GET /wild/" + strings.Repeat("x", i))
	}
	rows := h.RouteRowsFor("app-overflow")
	// Expect 50 real routes (reserved labels filtered out).
	if len(rows) != routeLabelSetCap {
		t.Errorf("rows: got %d, want %d (real labels only)", len(rows), routeLabelSetCap)
	}
	// All routes start with GET /wild/ — the overflow bucket label
	// itself must NOT appear in the real-routes list.
	for _, r := range rows {
		if r.Route == "__route_other__" {
			t.Errorf("__route_other__ should not appear when real routes exist")
		}
	}
}

// TestRouteRowsFor_CapHitOmitsOtherRouteRow pins that the
// __route_other__ overflow bucket never appears in the returned
// rows even when the app's routeLabelSet is overflowed. The
// auto-gen surface only cares about real customer-facing
// routes; the cap-hit signal is carried via the existing
// /v1/apps/{slug}/routes CapHit flag separately.
func TestRouteRowsFor_CapHitOmitsOtherRouteRow(t *testing.T) {
	h := makeTestHandler(t, "app-cap", []string{"GET /users", "GET /healthz", "GET /api/v1"})
	// Pre-admit 50 wildcards so the routeLabelSet is at cap
	// (overflowed=true) alongside the original 3 real routes.
	v, _ := h.routeSets.Load("app-cap")
	set := v.(*routeLabelSet)
	for i := 0; i < routeLabelSetCap; i++ {
		set.AdmitForTest("GET /wild/" + strings.Repeat("x", i))
	}
	rows := h.RouteRowsFor("app-cap")
	for _, r := range rows {
		if r.Route == "__route_other__" {
			t.Errorf("__route_other__ must not surface in auto-gen rows")
		}
	}
}

// TestRouteRowsFor_UnknownApp pins the "no routeLabelSet" path:
// the Handler has no set for the requested appID. RouteRowsFor
// returns nil so the apid handler normalises to [].
func TestRouteRowsFor_UnknownApp(t *testing.T) {
	h := makeTestHandler(t, "app-1", nil)
	rows := h.RouteRowsFor("app-NOPE")
	if rows != nil {
		t.Errorf("rows: got %+v, want nil", rows)
	}
}

// TestRouteRowsFor_NilHandler pins the nil-receiver-safe
// contract. Same nil-safety pattern as RoutesFor.
func TestRouteRowsFor_NilHandler(t *testing.T) {
	var h *Handler
	if rows := h.RouteRowsFor("app-1"); rows != nil {
		t.Errorf("nil handler should return nil; got %+v", rows)
	}
}

// TestRouteRowsFor_JoinsCounters pins the join with the
// per-route counter. Pre-admit a route, increment the counter
// several times across different status codes, and verify the
// returned Count is the sum across codes (the auto-gen
// surface counts total traffic, not per-code).
func TestRouteRowsFor_JoinsCounters(t *testing.T) {
	h := makeTestHandler(t, "app-1", []string{"GET /users"})
	// Increment the per-route counter across codes.
	for _, code := range []string{"200", "200", "200", "404"} {
		h.metrics.requestsByRoute.WithLabelValues("app-1", "free", "GET /users", code).Inc()
	}
	// Increment failures counter for the 404.
	h.metrics.failuresByRoute.WithLabelValues("app-1", "free", "GET /users", "404").Inc()
	rows := h.RouteRowsFor("app-1")
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	if rows[0].Count != 4 {
		t.Errorf("Count: got %d, want 4 (sum across codes)", rows[0].Count)
	}
	if rows[0].ErrorPct != 25.0 {
		t.Errorf("ErrorPct: got %v, want 25.0 (1 fail / 4 total)", rows[0].ErrorPct)
	}
}

// TestRouteRowsFor_IgnoresOtherApps pins the IDOR floor.
// Series for OTHER apps must NOT bleed into this app's row
// even when their routes collide by label value.
func TestRouteRowsFor_IgnoresOtherApps(t *testing.T) {
	h := makeTestHandler(t, "app-1", []string{"GET /users"})
	h.metrics.requestsByRoute.WithLabelValues("app-OTHER", "free", "GET /users", "200").Inc()
	h.metrics.requestsByRoute.WithLabelValues("app-OTHER", "free", "GET /users", "200").Inc()
	rows := h.RouteRowsFor("app-1")
	if len(rows) != 1 || rows[0].Count != 0 {
		t.Errorf("expected zero count for app-1 (other apps' series must not bleed); got %+v", rows)
	}
}

// TestQuantilesFromBuckets_MatchesPromQL pins the algorithm
// contract: linear interpolation between bucket boundaries,
// matching PromQL histogram_quantile within float epsilon.
//
// Reference: a histogram with 100 samples, 50 in (0.005, 0.01]
// and 50 in (0.05, 0.1] — p50 should land in (0.005, 0.01],
// p95 should land in (0.05, 0.1], p99 should be near 0.1.
func TestQuantilesFromBuckets_MatchesPromQL(t *testing.T) {
	buckets := []histogramBucket{
		{upperBound: 0.005, count: 0},
		{upperBound: 0.01, count: 50},   // 50% rank lands here
		{upperBound: 0.025, count: 50},  // (0.01, 0.025] empty
		{upperBound: 0.05, count: 50},   // (0.025, 0.05] empty
		{upperBound: 0.1, count: 100},   // 95% rank lands in (0.05, 0.1]
	}
	p50, p95, p99 := quantilesFromBuckets(buckets, 0.50, 0.95, 0.99)
	// p50: 50th sample = bucket boundary at 0.01s = 10ms.
	if !floatNear(p50, 10.0, 0.001) {
		t.Errorf("p50: got %v, want ~10ms", p50)
	}
	// p95: 95th sample lands in (0.05, 0.1] bucket; the bucket
	// is the only one with rank>50 → rank=95 falls inside.
	// Lower bound = 0.05s = 50ms; upper = 0.1s = 100ms.
	// Linear interpolation: 95/100 of the way through → 50 +
	// 0.9*(100-50) = 95ms.
	if !floatNear(p95, 95.0, 0.001) {
		t.Errorf("p95: got %v, want ~95ms", p95)
	}
	// p99: 99th sample also in (0.05, 0.1] bucket.
	// 99/100 of the way → 50 + 0.98*(100-50) = 99ms.
	if !floatNear(p99, 99.0, 0.001) {
		t.Errorf("p99: got %v, want ~99ms", p99)
	}
}

// TestQuantilesFromBuckets_Empty pins the no-data path.
func TestQuantilesFromBuckets_Empty(t *testing.T) {
	if p50, p95, p99 := quantilesFromBuckets(nil, 0.5, 0.95, 0.99); p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("empty buckets: got (%v, %v, %v), want (0, 0, 0)", p50, p95, p99)
	}
}

// TestQuantilesFromBuckets_AllZero pins the all-zero histogram.
func TestQuantilesFromBuckets_AllZero(t *testing.T) {
	buckets := []histogramBucket{{upperBound: 1.0, count: 0}}
	if p50, p95, p99 := quantilesFromBuckets(buckets, 0.5, 0.95, 0.99); p50 != 0 || p95 != 0 || p99 != 0 {
		t.Errorf("zero-count buckets: got (%v, %v, %v), want (0, 0, 0)", p50, p95, p99)
	}
}

// TestQuantilesFromBuckets_P100ReturnsLastBucketUB pins the
// p100 edge case — the last finite bucket's upper bound, NOT
// infinity (Prometheus caps histogram_quantile at the last
// finite bucket; we mirror that).
func TestQuantilesFromBuckets_P100ReturnsLastBucketUB(t *testing.T) {
	buckets := []histogramBucket{
		{upperBound: 0.1, count: 10},
		{upperBound: 1.0, count: 50},
	}
	_, _, p100 := quantilesFromBuckets(buckets, 0.5, 0.95, 1.0)
	if !floatNear(p100, 1000.0, 0.001) {
		t.Errorf("p100: got %v, want 1000ms (= last bucket UB)", p100)
	}
}

// TestQuantilesFromBuckets_NaNGuard pins the NaN/Inf guard.
// A pathological bucket configuration (zero-size bucket that
// divides by zero) must return 0 rather than NaN.
func TestQuantilesFromBuckets_NaNGuard(t *testing.T) {
	buckets := []histogramBucket{
		{upperBound: 1.0, count: 10},
		{upperBound: 2.0, count: 10}, // same count = zero-size bucket
	}
	// p50 lands at the boundary where the bucket is empty
	// between rank-10 and rank-10 → the function must NOT
	// return NaN.
	if v := quantileFromBuckets(buckets, 10, 0.5); math.IsNaN(v) {
		t.Errorf("got NaN; want non-NaN")
	}
}

// TestMergeHistogramBuckets_MergesByUpperBound pins the
// multi-class merge: two histograms with the same upper
// bounds produce a merged histogram whose bucket counts are
// the sum.
func TestMergeHistogramBuckets_MergesByUpperBound(t *testing.T) {
	b1 := histogramBucketCount(0.01, 5)
	b2 := histogramBucketCount(0.05, 7)
	b3 := histogramBucketCount(0.01, 3)
	merged := mergeHistogramBuckets([]histogramBucket{b1, b2}, histogramWith(b3))
	if len(merged) != 2 {
		t.Fatalf("merged len: got %d, want 2", len(merged))
	}
	// Find 0.01 bucket.
	for _, b := range merged {
		if b.upperBound == 0.01 && b.count != 8 {
			t.Errorf("0.01 bucket: got %d, want 8 (5+3)", b.count)
		}
		if b.upperBound == 0.05 && b.count != 7 {
			t.Errorf("0.05 bucket: got %d, want 7", b.count)
		}
	}
}

// TestAccumulateCounts_FilterByApp pins the app filter. Series
// not matching appID are dropped.
func TestAccumulateCounts_FilterByApp(t *testing.T) {
	mf := &dto.MetricFamily{
		Name: stringPtr("gateway_requests_by_route_total"),
		Metric: []*dto.Metric{
			counterMetric("app-1", "GET /users", 3),
			counterMetric("app-2", "GET /users", 99),
		},
	}
	out := map[string]uint64{}
	accumulateCounts(mf, "app-1", out)
	if out["GET /users"] != 3 {
		t.Errorf("filter: got %d, want 3 (app-2's 99 dropped)", out["GET /users"])
	}
}

// TestAccumulateFailures_ErrorPct pins the error_pct math.
// 1 failure / 4 total = 25%.
func TestAccumulateFailures_ErrorPct(t *testing.T) {
	mf := &dto.MetricFamily{
		Name: stringPtr("gateway_request_failures_by_route_total"),
		Metric: []*dto.Metric{
			counterMetric("app-1", "GET /users", 1),
		},
	}
	counts := map[string]uint64{"GET /users": 4}
	errPct := map[string]float64{}
	accumulateFailures(mf, "app-1", counts, errPct)
	if errPct["GET /users"] != 25.0 {
		t.Errorf("errPct: got %v, want 25.0", errPct["GET /users"])
	}
}

// TestAccumulateFailures_ZeroTotal pins the divide-by-zero
// guard: 0 failures / 0 total = 0%, not NaN.
func TestAccumulateFailures_ZeroTotal(t *testing.T) {
	mf := &dto.MetricFamily{
		Name: stringPtr("gateway_request_failures_by_route_total"),
		Metric: []*dto.Metric{
			counterMetric("app-1", "GET /users", 0),
		},
	}
	counts := map[string]uint64{}
	errPct := map[string]float64{}
	accumulateFailures(mf, "app-1", counts, errPct)
	v := errPct["GET /users"]
	if math.IsNaN(v) || v != 0 {
		t.Errorf("zero-total errPct: got %v, want 0 (not NaN)", v)
	}
}

// TestControlMuxRouteRows_OK pins the wire shape. The
// endpoint returns the documented envelope and the inner
// routes is the []api.RouteRow shape the apid handler
// expects.
func TestControlMuxRouteRows_OK(t *testing.T) {
	h := makeTestHandler(t, "app-1", []string{"GET /users"})
	mux := http.NewServeMux()
	ControlMuxRouteRows(mux, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/internal/apps/app-1/route-rows")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Faas-Routes-State"); got != "ok" {
		t.Errorf("X-Faas-Routes-State: got %q, want ok", got)
	}
	var env routeRowsResponseEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Slug != "app-1" || env.AppID != "app-1" {
		t.Errorf("envelope: got slug=%q appID=%q, want app-1/app-1", env.Slug, env.AppID)
	}
	if len(env.Routes) != 1 || env.Routes[0].Route != "GET /users" {
		t.Errorf("routes: got %+v, want single GET /users row", env.Routes)
	}
	// Pin the RouteRow wire field names via a marshal round-trip
	// on a populated row.
	wire := api.RouteRow{Route: "GET /x", Count: 7, P50MS: 12.5, ErrorPct: 1.5}
	b, _ := json.Marshal(wire)
	if !strings.Contains(string(b), `"route":"GET /x"`) {
		t.Errorf("RouteRow.Route wire name drift: got %s", b)
	}
	if !strings.Contains(string(b), `"count":7`) {
		t.Errorf("RouteRow.Count wire name drift: got %s", b)
	}
}

// TestControlMuxRouteRows_MethodNotAllowed pins the wrong-method
// contract — POST/PUT return 405 with Allow: GET.
func TestControlMuxRouteRows_MethodNotAllowed(t *testing.T) {
	h := makeTestHandler(t, "app-1", nil)
	mux := http.NewServeMux()
	ControlMuxRouteRows(mux, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/internal/apps/app-1/route-rows", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", resp.StatusCode)
	}
	if got := resp.Header.Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow: got %q, want GET", got)
	}
}

// TestControlMuxRouteRows_NilHandler pins the
// 503-no-handler-registered contract. Used in dev mode where
// the gatewayd daemon is not wired.
func TestControlMuxRouteRows_NilHandler(t *testing.T) {
	mux := http.NewServeMux()
	ControlMuxRouteRows(mux, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/internal/apps/app-1/route-rows")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", resp.StatusCode)
	}
}

// --- small test helpers ---

// floatNear reports whether a and b are within epsilon absolute.
func floatNear(a, b, epsilon float64) bool {
	return math.Abs(a-b) <= epsilon
}

// stringPtr returns &s — Go 1.23 stdlib doesn't have this in
// the dto package so we mirror it locally.
func stringPtr(s string) *string { return &s }

// counterMetric constructs a Counter metric with one app +
// one route label pair + a counter value. Used by
// accumulateCounts / accumulateFailures tests.
func counterMetric(app, route string, value float64) *dto.Metric {
	return &dto.Metric{
		Label: []*dto.LabelPair{
			{Name: stringPtr("app"), Value: stringPtr(app)},
			{Name: stringPtr("route"), Value: stringPtr(route)},
		},
		Counter: &dto.Counter{Value: float64Ptr(value)},
	}
}

// float64Ptr returns &v.
func float64Ptr(v float64) *float64 { return &v }

// histogramBucketCount is a small constructor used by the
// mergeHistogramBuckets test.
func histogramBucketCount(ub float64, count uint64) histogramBucket {
	return histogramBucket{upperBound: ub, count: count}
}

// histogramWith wraps a single bucket in a dto.Histogram for
// the mergeHistogramBuckets test path.
func histogramWith(b histogramBucket) *dto.Histogram {
	return &dto.Histogram{
		Bucket: []*dto.Bucket{
			{
				UpperBound:      float64Ptr(b.upperBound),
				CumulativeCount: uint64Ptr(b.count),
			},
		},
	}
}

// uint64Ptr returns &v.
func uint64Ptr(v uint64) *uint64 { return &v }

// NewMetricsForTest constructs a Metrics with a fresh
// in-process registry. Mirrors NewMetrics but with the
// dependencies stripped — only the per-route counters/histograms
// are wired (no fleet-level metrics, no signalbus, no
// hostnameLabelSet) since the test only exercises RouteRowsFor.
func NewMetricsForTest() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
	}
	m.requestsByRoute = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_requests_by_route_total",
		Help: "test",
	}, []string{"app", "plan", "route", "code"})
	m.failuresByRoute = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gateway_request_failures_by_route_total",
		Help: "test",
	}, []string{"app", "plan", "route", "code"})
	m.durationByRoute = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "gateway_request_duration_by_route_seconds",
		Help:    "test",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10},
	}, []string{"app", "route", "class"})
	m.registry.MustRegister(m.requestsByRoute, m.failuresByRoute, m.durationByRoute)
	return m
}
