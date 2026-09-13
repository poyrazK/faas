// range_test.go — Issue #696 / ADR-082 dashboard follow-up PR
// tests for the time-bucketed FetchRange /
// FetchRangeAccount helpers. Mirrors the existing
// appmetrics_test.go pattern (table-driven, stub PromQL,
// closed-set window guard).
//
// Coverage matrix:
//   - stepForWindow: 1h→1m, 24h→15m, 7d→1h, anything else→15m
//   - nil-fetcher → empty RangeSeries
//   - label-injection guard (crafted appID with `"`) → empty
//   - happy path: 3 latency + error + cold-boot series returned
//   - one query fails → empty sub-slice, the rest populate
package appmetrics_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
)

// stubRangeFetcher is the test double for appmetrics.RangeFetcher.
// fn is per-query; tests set the per-scenario behaviour. The
// return shape mirrors the production promql.Client.QueryRange
// (anonymous struct, one slice per series).
type stubRangeFetcher struct {
	fn func(query, start, end, step string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error)
}

func (s *stubRangeFetcher) QueryRange(_ context.Context, query, start, end, step string) ([]struct {
	Metric map[string]string
	Values []promql.QueryRangeSample
}, error) {
	return s.fn(query, start, end, step)
}

// makeSeries returns a flat list of samples with monotonically
// increasing timestamps starting at 1.0 (matching Prometheus's
// fractional-seconds shape). The values are the caller-supplied
// float slice.
func makeSeries(values []float64) []promql.QueryRangeSample {
	out := make([]promql.QueryRangeSample, 0, len(values))
	for i, v := range values {
		out = append(out, promql.QueryRangeSample{
			Timestamp: int64(i + 1), // 1s, 2s, 3s, …
			Value:     v,
		})
	}
	return out
}

// TestFetchRange_ClosedSetWindow pins the closed-set window
// vocabulary → step size mapping. The dashboard's <svg> width
// assumes the bucket count; a future change to the step param
// must update this test.
func TestFetchRange_ClosedSetWindow(t *testing.T) {
	// We don't need the fetcher to answer — the test asserts
	// the bucket-count tripwire by inspecting Step on the
	// returned RangeSeries.
	cases := []struct {
		window string
		step   string
	}{
		{"1h", "1m"},
		{"24h", "15m"},
		{"7d", "1h"},
		{"", "15m"},      // default falls back to 24h's step
		{"bogus", "15m"}, // defensive fallback
	}
	for _, c := range cases {
		// nil fetcher short-circuits before any query runs.
		got := appmetrics.FetchRange(context.Background(), nil, slog.Default(), "app-1", c.window)
		if got.Step != c.step {
			t.Errorf("window=%q: step=%q, want %q", c.window, got.Step, c.step)
		}
	}
}

// TestFetchRange_NilFetcherEmptySeries pins the nil-fetcher
// short-circuit: the helper returns an empty RangeSeries (no
// error) so the dashboard's empty-state badge can render
// without an explicit error branch.
func TestFetchRange_NilFetcherEmptySeries(t *testing.T) {
	got := appmetrics.FetchRange(context.Background(), nil, slog.Default(), "app-1", "24h")
	if got.Step != "15m" {
		t.Errorf("Step=%q, want 15m", got.Step)
	}
	if len(got.Latency.P50) != 0 || len(got.Latency.P95) != 0 || len(got.Latency.P99) != 0 {
		t.Errorf("latency: expected empty sub-slices, got %+v", got.Latency)
	}
	if len(got.ErrorRate) != 0 {
		t.Errorf("error rate: expected empty, got %d points", len(got.ErrorRate))
	}
	if len(got.ColdBootRate) != 0 {
		t.Errorf("cold boot: expected empty, got %d points", len(got.ColdBootRate))
	}
}

// TestFetchRange_LabelInjectionGuard pins the crafted-appID
// guard. A malicious caller-supplied appID containing `"` would
// close the outer label and re-open a new selector — the
// guard short-circuits to empty RangeSeries without invoking
// the fetcher.
func TestFetchRange_LabelInjectionGuard(t *testing.T) {
	called := false
	fetcher := &stubRangeFetcher{fn: func(_, _, _, _ string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error) {
		called = true
		return nil, nil
	}}
	got := appmetrics.FetchRange(context.Background(), fetcher, slog.Default(), `app-1" or vector(1) or app="`, "24h")
	if called {
		t.Errorf("fetcher was called for crafted appID; should have short-circuited")
	}
	if len(got.Latency.P95) != 0 {
		t.Errorf("latency p95: expected empty, got %d points", len(got.Latency.P95))
	}
}

// TestFetchRange_HappyPath pins the happy-path projection: the
// 5 calls (3 latency + error + cold-boot) each return a populated
// series; the helper projects each into the SparklinePoint slice.
func TestFetchRange_HappyPath(t *testing.T) {
	fetcher := &stubRangeFetcher{fn: func(query, _, _, _ string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error) {
		// Use the query string to decide which series to
		// return — mirrors the production branch logic.
		switch {
		case strings.Contains(query, "histogram_quantile(0.5"):
			return []struct {
				Metric map[string]string
				Values []promql.QueryRangeSample
			}{{Values: makeSeries([]float64{10, 11, 12})}}, nil
		case strings.Contains(query, "histogram_quantile(0.95"):
			return []struct {
				Metric map[string]string
				Values []promql.QueryRangeSample
			}{{Values: makeSeries([]float64{80, 85, 90})}}, nil
		case strings.Contains(query, "histogram_quantile(0.99"):
			return []struct {
				Metric map[string]string
				Values []promql.QueryRangeSample
			}{{Values: makeSeries([]float64{300, 310, 320})}}, nil
		case strings.Contains(query, "[45]xx"):
			return []struct {
				Metric map[string]string
				Values []promql.QueryRangeSample
			}{{Values: makeSeries([]float64{0.4, 0.5, 0.6})}}, nil
		case strings.Contains(query, "cold_boot_total"):
			return []struct {
				Metric map[string]string
				Values []promql.QueryRangeSample
			}{{Values: makeSeries([]float64{3, 4, 5})}}, nil
		}
		return nil, nil
	}}
	got := appmetrics.FetchRange(context.Background(), fetcher, slog.Default(), "app-1", "24h")
	if len(got.Latency.P50) != 3 || got.Latency.P50[1].Value != 11 {
		t.Errorf("latency p50: %+v", got.Latency.P50)
	}
	if len(got.Latency.P95) != 3 || got.Latency.P95[2].Value != 90 {
		t.Errorf("latency p95: %+v", got.Latency.P95)
	}
	if len(got.Latency.P99) != 3 || got.Latency.P99[0].Value != 300 {
		t.Errorf("latency p99: %+v", got.Latency.P99)
	}
	if len(got.ErrorRate) != 3 || got.ErrorRate[0].Value != 0.4 {
		t.Errorf("error rate: %+v", got.ErrorRate)
	}
	if len(got.ColdBootRate) != 3 || got.ColdBootRate[2].Value != 5 {
		t.Errorf("cold boot: %+v", got.ColdBootRate)
	}
}

// TestFetchRange_DegradedReturnsEmptySeries pins the per-query
// failure path: a single Prometheus call erroring should NOT
// blank the entire response — the remaining sub-slices populate
// so the dashboard renders the headline values it has.
func TestFetchRange_DegradedReturnsEmptySeries(t *testing.T) {
	fetcher := &stubRangeFetcher{fn: func(query, _, _, _ string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error) {
		if strings.Contains(query, "histogram_quantile(0.95") {
			return nil, errors.New("prometheus 500: out of memory")
		}
		// Other queries succeed.
		return []struct {
			Metric map[string]string
			Values []promql.QueryRangeSample
		}{{Values: makeSeries([]float64{1, 2, 3})}}, nil
	}}
	got := appmetrics.FetchRange(context.Background(), fetcher, slog.Default(), "app-1", "24h")
	if len(got.Latency.P95) != 0 {
		t.Errorf("p95 should be empty after Prometheus 500, got %+v", got.Latency.P95)
	}
	if len(got.Latency.P50) != 3 {
		t.Errorf("p50 should still populate, got %+v", got.Latency.P50)
	}
	if len(got.ErrorRate) != 3 {
		t.Errorf("error rate should still populate, got %+v", got.ErrorRate)
	}
}

// TestFetchRangeAccount_UsesClosedAppSet pins tenant isolation: every query
// includes the account's apps and excludes unrelated IDs.
func TestFetchRangeAccount_UsesClosedAppSet(t *testing.T) {
	queries := []string{}
	fetcher := &stubRangeFetcher{fn: func(query, _, _, _ string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error) {
		queries = append(queries, query)
		return []struct {
			Metric map[string]string
			Values []promql.QueryRangeSample
		}{{Values: makeSeries([]float64{1, 2})}}, nil
	}}
	_ = appmetrics.FetchRangeAccount(context.Background(), fetcher, slog.Default(), []string{"app-b", "app-a"}, "24h")
	for _, q := range queries {
		if !strings.Contains(q, `app=~"^(?:app-a|app-b)$"`) {
			t.Errorf("account query lacks closed app selector: %s", q)
		}
	}
}
