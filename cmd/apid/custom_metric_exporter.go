package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// customMetricExporter exports fresh ADR-202 gauges as
// gregale_app_custom_metric{app,name} (ADR-745). The platform Prometheus
// scrapes apid, so each pushed metric gains history without a new store.
// Stale values are omitted so a stopped pusher shows as a gap.
type customMetricExporter struct {
	store state.CustomMetricExportStore
	now   func() time.Time
	log   *slog.Logger
	desc  *prometheus.Desc

	mu       sync.Mutex
	cached   []state.CustomMetricSample
	cachedAt time.Time
}

func newCustomMetricExporter(store state.CustomMetricExportStore, now func() time.Time, log *slog.Logger) *customMetricExporter {
	return &customMetricExporter{
		store: store, now: now, log: log,
		desc: prometheus.NewDesc("gregale_app_custom_metric",
			"Latest fresh value an app pushed for a custom metric (ADR-202/ADR-745).",
			[]string{"app", "name"}, nil),
	}
}

func (e *customMetricExporter) Describe(ch chan<- *prometheus.Desc) { ch <- e.desc }

func (e *customMetricExporter) Collect(ch chan<- prometheus.Metric) {
	for _, sample := range e.samples() {
		ch <- prometheus.MustNewConstMetric(e.desc, prometheus.GaugeValue, sample.Value, sample.AppID, sample.Name)
	}
}

// samples returns the cached read when it is younger than the cache window.
// A failed read exports nothing rather than stale values.
func (e *customMetricExporter) samples() []state.CustomMetricSample {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cachedAt.IsZero() && now.Sub(e.cachedAt) < api.CustomMetricExportCacheSeconds*time.Second {
		return e.cached
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	since := now.Add(-api.CustomMetricFreshnessSeconds * time.Second)
	samples, err := e.store.ListFreshCustomMetrics(ctx, since, api.CustomMetricExportMaxSeries)
	if err != nil {
		if e.log != nil {
			e.log.Warn("apid: custom metric export read failed", "err", err)
		}
		e.cached = nil
	} else {
		e.cached = samples
	}
	e.cachedAt = now
	return e.cached
}
