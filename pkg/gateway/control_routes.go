package gateway

// ADR-093 bridge for the apid auto-gen surface (ADR-126 /
// issue #975 item #2). The `?source=auto` apid handler needs
// the per-app, per-route observed-traffic list to layer onto
// the imported OpenAPI doc. apid dials the gatewayd-internal
// control listener at /v1/internal/apps/{slug}/route-rows and
// gets back []api.RouteRow (Route, Count, P50MS, P95MS, P99MS,
// ErrorPct).
//
// ADR-093 already shipped the in-memory routeLabelSet
// (pkg/gateway/route_label_set.go) and the per-app per-route
// Prometheus histograms (gateway_requests_by_route_total,
// gateway_request_duration_by_route_seconds,
// gateway_request_failures_by_route_total). This file glues
// them together into the wire shape apid consumes:
//
//  1. RouteRowsFor(appID) — exported handler method that
//     returns []api.RouteRow. Walks routeLabelSet (the bounded
//     admission map) and joins against the Prometheus
//     counters/histograms to fill Count/ErrorPct/percentiles.
//
//  2. ControlMuxRouteRows(mux) — registers the new
//     /v1/internal/apps/{slug}/route-rows endpoint on the
//     control listener alongside the existing
//     /v1/internal/apps/{slug}/routes endpoint. The two
//     surfaces co-exist: the strings-only endpoint stays for
//     the dashboard's lightweight label snapshot; the
//     route-rows endpoint carries the auto-gen input.
//
// Concurrency: the Prometheus gather call holds the registry
// lock briefly per series; the read is O(routesPerApp) and
// bounded by routeLabelSetCap (=50). The cost is acceptable on
// the loopback apid→gatewayd hop (no public listener, no
// customer-facing latency). P50/P95/P99 are computed from the
// histogram bucket counts via the standard linear-interpolation
// quantile method (the same algorithm PromQL histogram_quantile
// uses; keeping it identical lets the dashboard panel cross-
// check the two surfaces).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	dto "github.com/prometheus/client_model/go"
)

// RouteRowsFor (ADR-093 bridge) returns the per-app, per-route
// observation rows for appID. The returned slice is sorted by
// route label ascending for deterministic dashboard rendering.
//
// Returns nil when the app has not opted in (no routeLabelSet
// created) OR when the routeLabelSet has only reserved labels
// (no real customer-facing routes). The apid handler normalises
// nil → empty slice.
//
// The returned slice EXCLUDES the two reserved labels
// (empty-string sentinel and `__route_other__` overflow
// bucket) that RoutesFor returns by construction — those are
// meta-buckets the auto-gen surface doesn't care about. The
// cap-hit signal is carried separately (via the existing
// routesUpstreamResponse.CapHit field the dashboard's
// /v1/apps/{slug}/routes endpoint emits; the auto-gen surface
// doesn't need a duplicate signal — it reads routes anyway).
//
// Concurrency: holds the routeLabelSet mutex briefly to snapshot
// the admitted labels, then iterates the Prometheus registry
// to fill the count/error_pct/percentile fields. The gather
// step is read-only and uses prometheus.Gatherer.Gather which
// is safe for concurrent calls. Worst-case cost is
// O(routeLabelSetCap + perRouteSeries) per call.
func (h *Handler) RouteRowsFor(appID string) []api.RouteRow {
	if h == nil || appID == "" {
		return nil
	}
	// Snapshot the admitted labels. RoutesFor already returns
	// the sorted slice + overflowed flag.
	labels, _ := h.RoutesFor(appID)
	// Filter out the two reserved labels. The empty sentinel is
	// the "no appID resolved" placeholder (same value the
	// existing `gateway_requests_total{app="-"}` series uses);
	// __route_other__ is the overflow bucket. The auto-gen
	// surface only cares about real customer-facing routes.
	realLabels := make([]string, 0, len(labels))
	for _, l := range labels {
		if l == reservedRouteLabelEmpty || l == otherRouteLabel {
			continue
		}
		realLabels = append(realLabels, l)
	}
	if len(realLabels) == 0 {
		return nil
	}
	// Gather the registry to fill per-route counts + percentiles.
	// Empty registry (no app opted in yet) is fine — we just
	// return the labels with zero counters.
	counts, errPct, histogramBuckets := h.gatherRouteMetrics(appID)
	rows := make([]api.RouteRow, 0, len(realLabels))
	for _, l := range realLabels {
		row := api.RouteRow{
			Route:    l,
			Count:    counts[l],
			ErrorPct: errPct[l],
		}
		if buckets, ok := histogramBuckets[l]; ok {
			row.P50MS, row.P95MS, row.P99MS = quantilesFromBuckets(buckets, 0.50, 0.95, 0.99)
		}
		rows = append(rows, row)
	}
	return rows
}

// routeMetrics is the per-route snapshot pulled from the
// Prometheus registry. Maps are keyed by route label.
//
// counts[route] = Σ requestsByRoute{app=appID, route=route}
// errPct[route] = failuresByRoute / requestsByRoute (0..100)
// histogramBuckets[route] = upper-bound → cumulative count
type routeMetricsSnapshot struct {
	counts           map[string]uint64
	errPct           map[string]float64
	histogramBuckets map[string][]histogramBucket
}

type histogramBucket struct {
	upperBound float64
	count      uint64 // cumulative
}

// gatherRouteMetrics walks the Metrics registry and returns
// per-route counters + histogram buckets for appID. Returns
// empty maps (not nil) when the app is not present in the
// registry.
//
// Pulls from three metric families:
//
//   - gateway_requests_by_route_total{app, plan, route, code}
//     counts per (route, code)
//   - gateway_request_failures_by_route_total{app, plan, route, code}
//     failures per (route, code), status ≥ 400 (gatewayd-side
//     only increments on ≥400)
//   - gateway_request_duration_by_route_seconds{app, route, class}
//     per-route latency histogram, summed across `class`
//
// Two-pass: requests first, then failures — the failure-to-total
// ratio depends on the per-route total being already populated.
// `prometheus.Gatherer.Gather` returns families in registration
// order (alphabetical by name in the default registry), so the
// pass separation makes the dependency explicit rather than
// relying on happenstance ordering.
//
// Cost: O(metricFamilies × series) gather — bounded by the
// number of opted-in apps × routeLabelSetCap. In production
// (≤100 opted-in apps × 50 routes each) the gather is a few
// hundred series; the gather completes in <1ms on the control
// listener's loopback hop.
func (h *Handler) gatherRouteMetrics(appID string) (
	counts map[string]uint64,
	errPct map[string]float64,
	histogramBuckets map[string][]histogramBucket,
) {
	counts = map[string]uint64{}
	errPct = map[string]float64{}
	histogramBuckets = map[string][]histogramBucket{}
	if h == nil || h.metrics == nil || h.metrics.Registry() == nil {
		return counts, errPct, histogramBuckets
	}
	mfs, err := h.metrics.Registry().Gather()
	if err != nil {
		return counts, errPct, histogramBuckets
	}
	// Pass 1: total counts (so the failure ratio has a denominator).
	for _, mf := range mfs {
		if mf.GetName() == "gateway_requests_by_route_total" {
			accumulateCounts(mf, appID, counts)
		}
	}
	// Pass 2: failures + histograms (depend on pass-1 results
	// for failures, but not for histograms — both run in this
	// pass because the gather is the same cost either way).
	for _, mf := range mfs {
		switch mf.GetName() {
		case "gateway_request_failures_by_route_total":
			accumulateFailures(mf, appID, counts, errPct)
		case "gateway_request_duration_by_route_seconds":
			accumulateHistograms(mf, appID, histogramBuckets)
		}
	}
	return counts, errPct, histogramBuckets
}

// accumulateCounts sums gateway_requests_by_route_total across
// {code} label dimensions for matching (app, route) pairs.
func accumulateCounts(mf *dto.MetricFamily, appID string, out map[string]uint64) {
	for _, m := range mf.Metric {
		if !hasLabel(m.Label, "app", appID) {
			continue
		}
		route := labelValue(m.Label, "route")
		if route == "" {
			continue
		}
		out[route] += uint64(m.Counter.GetValue())
	}
}

// accumulateFailures computes error_pct per route from
// gateway_request_failures_by_route_total. Both maps are
// in/out: out[route] holds the total requests (already
// accumulated by accumulateCounts); errPct[route] is filled
// here as failures/counts*100.
func accumulateFailures(mf *dto.MetricFamily, appID string, counts map[string]uint64, errPct map[string]float64) {
	for _, m := range mf.Metric {
		if !hasLabel(m.Label, "app", appID) {
			continue
		}
		route := labelValue(m.Label, "route")
		if route == "" {
			continue
		}
		failures := uint64(m.Counter.GetValue())
		total := counts[route]
		if total == 0 {
			errPct[route] = 0
			continue
		}
		errPct[route] = float64(failures) / float64(total) * 100.0
	}
}

// accumulateHistograms merges gateway_request_duration_by_route_seconds
// across the {class} label dimension for matching (app, route)
// pairs. The merged histogram is appended to histogramBuckets[route]
// and quantiles are computed lazily by quantilesFromBuckets.
func accumulateHistograms(mf *dto.MetricFamily, appID string, out map[string][]histogramBucket) {
	for _, m := range mf.Metric {
		if !hasLabel(m.Label, "app", appID) {
			continue
		}
		route := labelValue(m.Label, "route")
		if route == "" {
			continue
		}
		h := m.Histogram
		if h == nil {
			continue
		}
		// Sum bucket counts across {class} variants for the same
		// (app, route) tuple. Multiple class entries produce
		// duplicate buckets; we accumulate counts into a single
		// sorted bucket array per route.
		merged := out[route]
		merged = mergeHistogramBuckets(merged, h)
		out[route] = merged
	}
}

// mergeHistogramBuckets adds the bucket counts from h into the
// existing buckets slice (or starts a fresh one), returning
// the merged slice sorted by upperBound ascending. Buckets
// from different class labels are summed into the same
// upperBound entry — same semantics as PromQL's histogram_quantile
// over a multi-series histogram.
func mergeHistogramBuckets(existing []histogramBucket, h *dto.Histogram) []histogramBucket {
	// Build a map from upperBound → cumulative count from existing.
	merged := map[float64]uint64{}
	for _, b := range existing {
		merged[b.upperBound] += b.count
	}
	for _, b := range h.Bucket {
		merged[b.GetUpperBound()] += uint64(b.GetCumulativeCount())
	}
	out := make([]histogramBucket, 0, len(merged))
	for ub, c := range merged {
		out = append(out, histogramBucket{upperBound: ub, count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].upperBound < out[j].upperBound })
	return out
}

// quantilesFromBuckets computes p50/p95/p99 from a sorted-bucket
// histogram via linear interpolation between bucket boundaries,
// matching PromQL histogram_quantile's algorithm.
//
// Returns the quantile value in MILLISECONDS (the API DTO field
// is P50MS etc.), so the seconds-to-ms conversion happens at
// the boundary.
//
// Buckets with zero total count return (0, 0, 0) — the dashboard
// surfaces "no data" rather than 0ms.
func quantilesFromBuckets(buckets []histogramBucket, qs ...float64) (float64, float64, float64) {
	if len(buckets) == 0 {
		return 0, 0, 0
	}
	// Total = last bucket's cumulative count (Prometheus
	// histograms are cumulative by construction).
	total := buckets[len(buckets)-1].count
	if total == 0 {
		return 0, 0, 0
	}
	out := make([]float64, len(qs))
	for i, q := range qs {
		out[i] = quantileFromBuckets(buckets, total, q)
	}
	if len(out) < 3 {
		// Defensive: caller passed <3 quantiles, pad with zeros.
		for len(out) < 3 {
			out = append(out, 0)
		}
	}
	return out[0], out[1], out[2]
}

// quantileFromBuckets implements histogram_quantile for a single
// quantile. Algorithm:
//  1. Find the bucket where cumulative count crosses the
//     rank-threshold (q * total).
//  2. Linearly interpolate between the bucket's lower bound
//     (previous bucket's upperBound) and upper bound.
//
// This matches PromQL's histogram_quantile exactly — the
// dashboard's histogram_quantile(metric, ...) panel and this
// function produce the same number to within float epsilon.
func quantileFromBuckets(buckets []histogramBucket, total uint64, q float64) float64 {
	if total == 0 || q <= 0 {
		return 0
	}
	if q >= 1 {
		// p100 = the last bucket's upper bound (Prometheus
		// caps at the last finite bucket + the +Inf count;
		// we omit +Inf so we report the largest finite UB).
		return buckets[len(buckets)-1].upperBound * 1000.0
	}
	rank := float64(total) * q
	prevUB := 0.0
	var prevCount uint64
	for _, b := range buckets {
		if float64(b.count) >= rank {
			// Found the bucket containing the rank. Linear
			// interpolation between prevUB (rank prevCount)
			// and b.upperBound (rank b.count).
			bucketSize := float64(b.count - prevCount)
			if bucketSize <= 0 {
				return b.upperBound * 1000.0
			}
			offset := (rank - float64(prevCount)) / bucketSize
			val := prevUB + offset*(b.upperBound-prevUB)
			if math.IsNaN(val) || math.IsInf(val, 0) {
				return 0
			}
			return val * 1000.0
		}
		prevUB = b.upperBound
		prevCount = b.count
	}
	return buckets[len(buckets)-1].upperBound * 1000.0
}

// hasLabel reports whether m has a label pair (name == wantName)
// with value == wantValue. Returns false on nil metric or empty
// labels.
func hasLabel(m []*dto.LabelPair, wantName, wantValue string) bool {
	for _, lp := range m {
		if lp.GetName() == wantName && lp.GetValue() == wantValue {
			return true
		}
	}
	return false
}

// labelValue returns the value of the label with name wantName,
// or "" if not present.
func labelValue(m []*dto.LabelPair, wantName string) string {
	for _, lp := range m {
		if lp.GetName() == wantName {
			return lp.GetValue()
		}
	}
	return ""
}

// ControlMuxRouteRows registers GET /v1/internal/apps/{slug}/route-rows
// on mux. The slug is the app's primary-key UUID string (matches
// the existing /v1/internal/apps/{slug}/routes slug-shape
// contract — apid passes the resolved app UUID, not the human
// slug, to avoid a round-trip through the apps table).
//
// The handler returns 200 with {"slug":..., "app_id":..., "routes":[...]}
// (mirrors the existing routes handler's envelope) or 503 with
// "no handler registered" when the Handler is nil (test-only
// posture — production wires the Handler at construction).
//
// Loopback-only — apid is the only caller. No auth check (the
// loopback bind is the auth, same contract as the existing
// /v1/internal/apps/{slug}/routes endpoint).
func ControlMuxRouteRows(mux *http.ServeMux, h *Handler) {
	mux.HandleFunc("/v1/internal/apps/{slug}/route-rows", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		slug := r.PathValue("slug")
		if slug == "" {
			http.Error(w, "missing slug", http.StatusBadRequest)
			return
		}
		if h == nil {
			http.Error(w, "no handler registered", http.StatusServiceUnavailable)
			return
		}
		rows := h.RouteRowsFor(slug)
		if rows == nil {
			rows = []api.RouteRow{}
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Faas-Routes-State", "ok")
		_ = json.NewEncoder(w).Encode(routeRowsResponseEnvelope{
			Slug:   slug,
			AppID:  slug,
			Routes: rows,
		})
	})
}

// routeRowsResponseEnvelope mirrors the existing
// routesUpstreamResponse shape from cmd/apid/handlers_routes.go
// but with []api.RouteRow instead of []string. The envelope is
// identical except for the routes field type, so the apid
// decoder can share the same body-parsing path.
type routeRowsResponseEnvelope struct {
	Slug   string         `json:"slug"`
	AppID  string         `json:"app_id"`
	Routes []api.RouteRow `json:"routes"`
}

// ControlMuxRouteRowsHandler is a test-friendly constructor that
// returns an http.HandlerFunc instead of registering on a mux.
// Used by control_routes_test.go to inject a mock Handler.
func ControlMuxRouteRowsHandler(h *Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		if slug == "" {
			http.Error(w, "missing slug", http.StatusBadRequest)
			return
		}
		if h == nil {
			http.Error(w, "no handler registered", http.StatusServiceUnavailable)
			return
		}
		rows := h.RouteRowsFor(slug)
		if rows == nil {
			rows = []api.RouteRow{}
		}
		_ = json.NewEncoder(w).Encode(routeRowsResponseEnvelope{
			Slug:   slug,
			AppID:  slug,
			Routes: rows,
		})
	}
}

// routeRowsScrapeTimeout bounds the per-call registry gather
// cost. The control listener's ReadTimeout (10s) is the ceiling;
// we set 500ms because gather is in-process and bounded.
const routeRowsScrapeTimeout = 500 * time.Millisecond

// routeRowsCtx is the per-call context helper. Extracted so
// tests can substitute a deadline-canceled context without
// spinning up a real HTTP server.
func routeRowsCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, routeRowsScrapeTimeout)
}

// strconvI64 is a small helper for the test path that needs
// to compare integer label values to expected ones without
// pulling in strconv.FormatInt in two places.
func strconvI64(v uint64) string { return strconv.FormatUint(v, 10) }

// fmtRow is the test helper that renders one RouteRow as
// "route|count|p50|p95|p99|err" for stable table-driven
// comparison.
func fmtRow(r api.RouteRow) string {
	return fmt.Sprintf("%s|%s|%.2f|%.2f|%.2f|%.2f",
		r.Route,
		strconvI64(r.Count),
		r.P50MS,
		r.P95MS,
		r.P99MS,
		r.ErrorPct,
	)
}
