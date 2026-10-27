package appmetrics_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
)

type windowFetcher struct {
	count, average float64
	err            error
	queries        []string
}

func (f *windowFetcher) QueryScalar(_ context.Context, query string) (float64, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return 0, f.err
	}
	if strings.Contains(query, "count_over_time") {
		return f.count, nil
	}
	return f.average, nil
}

// adr: 20261019071734 amendment 1 — a custom_metric gate reads its window
// from the exported history, pinned to the release window's end.
func TestCustomMetricWindow(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	until := since.Add(5 * time.Minute)

	f := &windowFetcher{count: 20, average: 41.5}
	samples, average, err := appmetrics.NewCustomMetricHistory(f).CustomMetricWindow(context.Background(), "app-1", "queue_depth", since, until)
	if err != nil || samples != 20 || average != 41.5 {
		t.Fatalf("window = %d, %v, %v; want 20 samples averaging 41.5", samples, average, err)
	}
	want := `gregale_app_custom_metric{app="app-1",name="queue_depth"}[300s] @ 1800000300`
	if len(f.queries) != 2 || !strings.Contains(f.queries[0], "count_over_time("+want+")") || !strings.Contains(f.queries[1], "avg_over_time("+want+")") {
		t.Fatalf("queries = %q, want count and average over %s", f.queries, want)
	}

	empty := &windowFetcher{count: 0}
	if samples, _, err := appmetrics.NewCustomMetricHistory(empty).CustomMetricWindow(context.Background(), "app-1", "queue_depth", since, until); err != nil || samples != 0 || len(empty.queries) != 1 {
		t.Fatalf("empty window = %d samples, %v, %d queries; want 0 samples after one query", samples, err, len(empty.queries))
	}

	failing := &windowFetcher{err: errors.New("prometheus down")}
	if _, _, err := appmetrics.NewCustomMetricHistory(failing).CustomMetricWindow(context.Background(), "app-1", "queue_depth", since, until); err == nil {
		t.Fatal("a failed query must make the evidence unavailable")
	}

	if _, _, err := appmetrics.NewCustomMetricHistory(f).CustomMetricWindow(context.Background(), `app"1`, "queue_depth", since, until); err == nil {
		t.Fatal("a selector with a quote must be refused")
	}

	var nilClient *promql.Client
	if appmetrics.NewCustomMetricHistory(nil) != nil || appmetrics.NewCustomMetricHistory(nilClient) != nil {
		t.Fatal("no Prometheus means no history reader")
	}
}
