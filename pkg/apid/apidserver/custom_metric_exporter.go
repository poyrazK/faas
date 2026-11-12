package apidserver

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
//
// Labelled series (ADR-20261108160000 slice C) are exported as
// gregale_app_custom_metric_series{app,name,labels}, labels being the
// series' canonical "k=v,..." key, so one fixed label set covers any
// customer labels. The name's aggregate stays in gregale_app_custom_metric.
type customMetricExporter struct {
	store state.CustomMetricExportStore
	now   func() time.Time
	log   *slog.Logger
	desc  *prometheus.Desc
	// seriesDesc is nil when the store has no series.
	seriesDesc *prometheus.Desc

	mu           sync.Mutex
	cached       []state.CustomMetricSample
	cachedSeries []state.CustomMetricSeriesSample
	cachedAt     time.Time
}

func newCustomMetricExporter(store state.CustomMetricExportStore, now func() time.Time, log *slog.Logger) *customMetricExporter {
	e := &customMetricExporter{
		store: store, now: now, log: log,
		desc: prometheus.NewDesc("gregale_app_custom_metric",
			"Latest fresh value an app pushed for a custom metric (ADR-202/ADR-745).",
			[]string{"app", "name"}, nil),
	}
	if _, ok := store.(state.CustomMetricSeriesStore); ok {
		e.seriesDesc = prometheus.NewDesc("gregale_app_custom_metric_series",
			"Latest fresh value of one labelled series of an app's custom metric (ADR-20261108160000).",
			[]string{"app", "name", "labels"}, nil)
	}
	return e
}

func (e *customMetricExporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- e.desc
	if e.seriesDesc != nil {
		ch <- e.seriesDesc
	}
}

func (e *customMetricExporter) Collect(ch chan<- prometheus.Metric) {
	samples, series := e.samples()
	for _, sample := range samples {
		ch <- prometheus.MustNewConstMetric(e.desc, prometheus.GaugeValue, sample.Value, sample.AppID, sample.Name)
	}
	for _, s := range series {
		ch <- prometheus.MustNewConstMetric(e.seriesDesc, prometheus.GaugeValue, s.Value, s.AppID, s.Name, s.SeriesKey)
	}
}

// samples returns the cached read when it is younger than the cache window.
// A failed read exports nothing rather than stale values.
func (e *customMetricExporter) samples() ([]state.CustomMetricSample, []state.CustomMetricSeriesSample) {
	now := e.now()
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.cachedAt.IsZero() && now.Sub(e.cachedAt) < api.CustomMetricExportCacheSeconds*time.Second {
		return e.cached, e.cachedSeries
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	since := now.Add(-api.CustomMetricFreshnessSeconds * time.Second)
	samples, err := e.store.ListFreshCustomMetrics(ctx, since, api.CustomMetricExportMaxSeries)
	if err != nil {
		e.warn(err)
		samples = nil
	}
	var series []state.CustomMetricSeriesSample
	if store, ok := e.store.(state.CustomMetricSeriesStore); ok && e.seriesDesc != nil {
		series, err = store.ListFreshCustomMetricSeries(ctx, since, api.CustomMetricExportMaxSeries)
		if err != nil {
			e.warn(err)
			series = nil
		}
	}
	e.cached, e.cachedSeries, e.cachedAt = samples, series, now
	return e.cached, e.cachedSeries
}

func (e *customMetricExporter) warn(err error) {
	if e.log != nil {
		e.log.Warn("apid: custom metric export read failed", "err", err)
	}
}
