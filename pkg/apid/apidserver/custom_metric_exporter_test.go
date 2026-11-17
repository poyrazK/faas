package apidserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestCustomMetricExporterExportsOnlyFreshValues is the capability
// acceptance test (pkg/productcap/catalog.json, ADR-745).
func TestCustomMetricExporterExportsOnlyFreshValues(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := store.PutCustomMetric(ctx, "app-a", "orders_pending", 42, now.Add(-time.Minute), 5); err != nil {
		t.Fatal(err)
	}
	if err := store.PutCustomMetric(ctx, "app-b", "ocr_backlog", 7, now.Add(-10*time.Minute), 5); err != nil {
		t.Fatal(err)
	}
	exporter := newCustomMetricExporter(store, func() time.Time { return now }, nil)
	reg := prometheus.NewRegistry()
	reg.MustRegister(exporter)

	want := `
# HELP gregale_app_custom_metric Latest fresh value an app pushed for a custom metric (ADR-202/ADR-745).
# TYPE gregale_app_custom_metric gauge
gregale_app_custom_metric{app="app-a",name="orders_pending"} 42
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "gregale_app_custom_metric"); err != nil {
		t.Fatalf("export (the 10-minute-old ocr_backlog must be omitted): %v", err)
	}
}

func TestCustomMetricExporterCachesWithinScrapeWindow(t *testing.T) {
	store := &countingExportStore{samples: []state.CustomMetricSample{{AppID: "a", Name: "n", Value: 1}}}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	exporter := newCustomMetricExporter(store, func() time.Time { return now }, nil)
	exporter.samples()
	exporter.samples()
	if store.reads != 1 {
		t.Fatalf("reads = %d, want one read per cache window", store.reads)
	}
	now = now.Add(20 * time.Second)
	exporter.samples()
	if store.reads != 2 {
		t.Fatalf("reads = %d, want a fresh read after the window", store.reads)
	}
}

// adr: 20261108160000 — slice C: fresh labelled series are exported with
// their canonical key, next to the metric's aggregate.
func TestCustomMetricExporterExportsLabelledSeries(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now()
	lim := state.CustomMetricSeriesLimits{Names: 5, SeriesPerMetric: 5, SeriesPerApp: 5, GaugeFreshness: 5 * time.Minute}
	for key, v := range map[string]float64{"queue=a": 3, "queue=b": 7} {
		if err := store.PutCustomMetricSeries(context.Background(), "app-1", state.CustomMetricSeriesWrite{
			Name: "depth", SeriesKey: key, Labels: map[string]string{"queue": key[6:]}, Kind: state.CustomMetricKindGauge, Value: v, ObservedAt: now,
		}, lim); err != nil {
			t.Fatal(err)
		}
	}
	exporter := newCustomMetricExporter(store, func() time.Time { return now }, nil)
	want := `
# HELP gregale_app_custom_metric Latest fresh value an app pushed for a custom metric (ADR-202/ADR-745).
# TYPE gregale_app_custom_metric gauge
gregale_app_custom_metric{app="app-1",name="depth"} 7
# HELP gregale_app_custom_metric_series Latest fresh value of one labelled series of an app's custom metric (ADR-20261108160000).
# TYPE gregale_app_custom_metric_series gauge
gregale_app_custom_metric_series{app="app-1",labels="queue=a",name="depth"} 3
gregale_app_custom_metric_series{app="app-1",labels="queue=b",name="depth"} 7
`
	if err := testutil.CollectAndCompare(exporter, strings.NewReader(want)); err != nil {
		t.Fatal(err)
	}
}

func TestCustomMetricExporterExportsNothingOnReadFailure(t *testing.T) {
	store := &countingExportStore{err: errors.New("db down")}
	exporter := newCustomMetricExporter(store, time.Now, nil)
	if got, _ := exporter.samples(); len(got) != 0 {
		t.Fatalf("a failed read must export nothing, got %v", got)
	}
}

type countingExportStore struct {
	samples []state.CustomMetricSample
	err     error
	reads   int
}

func (c *countingExportStore) ListFreshCustomMetrics(context.Context, time.Time, int) ([]state.CustomMetricSample, error) {
	c.reads++
	return c.samples, c.err
}
