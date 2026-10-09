package main

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

func TestCustomMetricExporterExportsNothingOnReadFailure(t *testing.T) {
	store := &countingExportStore{err: errors.New("db down")}
	exporter := newCustomMetricExporter(store, time.Now, nil)
	if got := exporter.samples(); len(got) != 0 {
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
