// Package promql is a thin HTTP client for the Prometheus query API
// used by apid for both the public status page (cmd/apid/status.go)
// and the per-app metrics endpoint behind issue #273 / ADR-042.
//
// Extracted from cmd/apid/status.go so two consumers (the existing
// /status/slo.json handler and the new GET /v1/apps/{slug}/metrics
// handler) share one tested transport and one sealed seam. The seam
// takes an HTTPDoer so tests can use httptest.Server without
// depending on http.DefaultClient's state.
package promql

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// HTTPDoer is the minimal interface Client needs from net/http. Matches
// http.Client.Do so the production wiring is `http.DefaultClient`. Tests
// pass httptest.Server.Client() which also satisfies it.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is a Prometheus query client bound to one base URL. Construct
// with NewClient; do not zero-initialise (the http.Client would be nil).
type Client struct {
	baseURL string
	doer    HTTPDoer
	timeout time.Duration
}

// NewClient builds a client. baseURL is the Prometheus root (no
// trailing slash, no /api/v1 suffix). doer is the http.Client
// (production: nil → 3s-timeout client; tests: an httptest.Server
// client). timeout is the per-query context timeout; pass 0 for the
// 3s default.
func NewClient(baseURL string, doer HTTPDoer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: 3 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), doer: doer, timeout: 3 * time.Second}
}

// SetTimeout overrides the per-query timeout. Use for slow queries in
// tests; production uses the 3s default.
func (c *Client) SetTimeout(d time.Duration) { c.timeout = d }

// BaseURL returns the configured Prometheus base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// QueryScalar runs query against Prometheus and returns the first
// scalar. Returns an error on transport failure, non-2xx, parse
// error, unsupported resultType (only vector and scalar are valid
// here), empty result, or a non-finite sample. Prometheus uses NaN
// for histogram queries with no observations; returning it would
// poison JSON responses assembled by callers.
//
// PromQL has three result shapes; only "scalar" and "vector" can
// appear for the instant-query API endpoint we call, and they're
// encoded differently in the JSON envelope:
//   - vector → {"resultType":"vector", "result":[{"value":[ts,"x"]}, ...]}
//   - scalar → {"resultType":"scalar", "result":[{"value":[ts,"x"]}]}
//   - matrix → {"resultType":"matrix", "result":[{"values":[...]}], ...}
//
// Both vector and scalar must be supported because e.g.
// `count(ALERTS{alertstate="firing"}) > 0` returns a scalar (the
// §12 degraded flag depends on this). The Value[0] is a timestamp in
// both shapes; the parsed scalar lives at Value[1].
func (c *Client) QueryScalar(ctx context.Context, query string) (float64, error) {
	if c == nil || c.baseURL == "" {
		return 0, fmt.Errorf("promql: client not configured")
	}
	qctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u := c.baseURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(qctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.doer.Do(req)
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
	if pr.Data.ResultType != "vector" && pr.Data.ResultType != "scalar" {
		return 0, fmt.Errorf("unsupported resultType %q for query %q", pr.Data.ResultType, query)
	}
	if len(pr.Data.Result) == 0 {
		return 0, fmt.Errorf("no data for query %q", query)
	}
	return parseSampleValue(pr.Data.Result[0].Value, query)
}

// queryResponse is the JSON envelope Prometheus returns for the
// instant-query API. Decoded by both QueryScalar (already inlined)
// and the new vector-query helpers — extracted here so all three
// share the same wire shape.
type queryResponse struct {
	Data struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// QueryRangeSample is one step in a Prometheus query_range response.
// Timestamp is the bucket-start time in nanoseconds (Prometheus
// emits seconds-as-float; the renderer converts at the call site).
// Value is the parsed sample value. The empty / no-data case is
// represented by two zero values — callers that need to distinguish
// "zero" from "no data" must check the enclosing slice length.
type QueryRangeSample struct {
	Timestamp int64
	Value     float64
}

// QueryRange runs query against Prometheus's query_range endpoint
// and returns one slice of samples per series. step controls the
// bucket size (e.g. "1m" / "5m" / "1h"). start / end are epoch
// seconds.
//
// The endpoint is /api/v1/query_range?query=…&start=…&end=…&step=….
// Prometheus emits:
//
//	{"data":{"resultType":"matrix",
//	          "result":[{"metric":{...},"values":[[ts,"x"],…]}]}}
//
// Series are returned in no particular order; multiple series
// occur when the query is labelled (e.g. `sum by (class)`). The
// caller picks the series it wants by its Metric label set.
//
// step is passed verbatim — Prometheus accepts Go-style durations
// ("30s", "1m", "1h", "1d", "1w"). The dashboard's range fetcher
// computes step from the window so the bucket count stays bounded
// for the 168-bucket 7d view.
//
// Transport / error handling mirrors QueryScalar: 3s default
// timeout, non-200 → truncated body in the error, non-matrix
// resultType → error with the offending query.
func (c *Client) QueryRange(ctx context.Context, query, start, end, step string) ([]struct {
	Metric map[string]string
	Values []QueryRangeSample
}, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("promql: client not configured")
	}
	qctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u := c.baseURL + "/api/v1/query_range?query=" + url.QueryEscape(query) +
		"&start=" + url.QueryEscape(start) +
		"&end=" + url.QueryEscape(end) +
		"&step=" + url.QueryEscape(step)
	req, err := http.NewRequestWithContext(qctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<12))
		return nil, fmt.Errorf("prometheus %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var pr struct {
		Data struct {
			ResultType string `json:"resultType"`
			Result     []struct {
				Metric map[string]string `json:"metric"`
				Values [][]any           `json:"values"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("expected matrix, got %q for query %q", pr.Data.ResultType, query)
	}
	out := make([]struct {
		Metric map[string]string
		Values []QueryRangeSample
	}, 0, len(pr.Data.Result))
	for _, row := range pr.Data.Result {
		samples := make([]QueryRangeSample, 0, len(row.Values))
		for _, v := range row.Values {
			if len(v) != 2 {
				continue
			}
			ts, ok := v[0].(float64)
			if !ok {
				continue
			}
			raw, ok := v[1].(string)
			if !ok {
				continue
			}
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				continue
			}
			samples = append(samples, QueryRangeSample{Timestamp: int64(ts), Value: f})
		}
		out = append(out, struct {
			Metric map[string]string
			Values []QueryRangeSample
		}{Metric: row.Metric, Values: samples})
	}
	return out, nil
}

// QueryMap runs query against Prometheus and returns the result as a
// map keyed by the `app` label on each vector sample. Used by the
// account-scoped metrics rollup (issue #393) so N apps cost one
// round-trip per metric (vs. N with QueryScalar). The query MUST
// produce a vector — matrix / scalar results error out, same as
// QueryScalar's contract.
//
// Sample without an `app` label is silently dropped (and so are
// samples whose label resolves to ""): the rollup's "6 round-trips
// regardless of N apps" promise depends on the per-app loop only
// seeing per-app keys; an unlabeled metric would otherwise poison
// the loop with a "" key. NaN / Inf samples parse to NaN / +Inf —
// callers guard with safeFloat / safeRoundNonNeg.
func (c *Client) QueryMap(ctx context.Context, query string) (map[string]float64, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("promql: client not configured")
	}
	rows, err := c.doVector(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, row := range rows {
		app, ok := row.Metric["app"]
		if !ok || app == "" {
			continue
		}
		f, err := parseSampleValue(row.Value, query)
		if err != nil {
			return nil, err
		}
		out[app] = f
	}
	return out, nil
}

// Buckets groups histogram-bucket samples by app. Returned shape:
// map[appID]map[le]float64. Used by the metrics rollup so a single
// `sum by (app, le) (rate(...))` query replaces N per-app `rate`
// queries; the caller runs histogram_quantile in Go per app.
//
// `le` is parsed as a float64 — Prometheus emits it as a string
// ("+Inf" or numeric). "+Inf" parses to +Inf (ParseFloat accepts
// "Inf" / "+Inf" / "-Inf"). The caller is responsible for handling
// +Inf per the histogram_quantile spec.
func (c *Client) QueryBuckets(ctx context.Context, query string) (map[string]map[string]float64, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("promql: client not configured")
	}
	rows, err := c.doVector(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]float64, len(rows))
	for _, row := range rows {
		app, ok := row.Metric["app"]
		if !ok || app == "" {
			continue
		}
		le, ok := row.Metric["le"]
		if !ok {
			continue
		}
		f, err := parseSampleValue(row.Value, query)
		if err != nil {
			return nil, err
		}
		bucket, exists := out[app]
		if !exists {
			bucket = make(map[string]float64, 4)
			out[app] = bucket
		}
		bucket[le] = f
	}
	return out, nil
}

// QueryGrouped (issue #881 / ADR-091 D20.5 amendment) mirrors
// QueryBuckets but with a caller-supplied inner label instead of
// the hardcoded `le`. Returned shape:
//
//	map[outerLabel]map[innerLabel]float64
//
// Used by pkg/throttlerec to pull per-route observed_rps values:
// the query is `sum by (app, route) (rate(...))` and the outer
// label is `app`, the inner label is `route`. The returned
// `app → route → rps` shape matches the ADT the recommender
// walks (one suggestion per (appID, route) pair, plus a
// `__route_other__` overflow bucket per the ADR-093 cap pattern).
//
// Routes where the inner label is missing (Prometheus carries
// no series without the by-clause's full label set) are dropped
// silently — the caller's expectation is that some routes will
// have zero observations for a given range, and that's normal.
//
// The metric label hardcoding stays in QueryMap + QueryBuckets
// (`app`) — these are existing call sites that depend on the
// shape and don't need widening. QueryGrouped is the new seam
// for queries that group by something other than the standard
// `app` label, and takes the label name explicitly so a future
// `pkg/something/fetcher` doesn't have to redefine the signature.
func (c *Client) QueryGrouped(ctx context.Context, query, outerLabel, innerLabel string) (map[string]map[string]float64, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("promql: client not configured")
	}
	if outerLabel == "" || innerLabel == "" {
		return nil, fmt.Errorf("promql: QueryGrouped requires non-empty outer/inner labels")
	}
	rows, err := c.doVector(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]float64, len(rows))
	for _, row := range rows {
		outer, ok := row.Metric[outerLabel]
		if !ok || outer == "" {
			continue
		}
		inner, ok := row.Metric[innerLabel]
		if !ok || inner == "" {
			continue
		}
		f, err := parseSampleValue(row.Value, query)
		if err != nil {
			return nil, err
		}
		bucket, exists := out[outer]
		if !exists {
			bucket = make(map[string]float64, 4)
			out[outer] = bucket
		}
		bucket[inner] = f
	}
	return out, nil
}

// doVector runs query and decodes a vector response into the shared
// queryResponse shape. Shared by QueryMap and QueryBuckets so the
// transport / parsing / error mapping is one tested path.
func (c *Client) doVector(ctx context.Context, query string) ([]struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}, error) {
	qctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	u := c.baseURL + "/api/v1/query?query=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(qctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("prometheus %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var pr queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}
	if pr.Data.ResultType != "vector" {
		return nil, fmt.Errorf("expected vector, got %q for query %q", pr.Data.ResultType, query)
	}
	return pr.Data.Result, nil
}

// parseSampleValue parses the second element of a Prometheus sample
// value pair (timestamp + stringified float). Returns a structured
// error so callers can wrap with the offending query.
func parseSampleValue(v [2]any, query string) (float64, error) {
	raw, ok := v[1].(string)
	if !ok {
		return 0, fmt.Errorf("unexpected value shape for query %q", query)
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q for query %q: %w", raw, query, err)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("non-finite value %q for query %q", raw, query)
	}
	return f, nil
}
