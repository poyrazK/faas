package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

func registerSchedulerMetrics(mux *http.ServeMux, ops, dashboard *prometheus.Registry) {
	// process_start_time_seconds lets FaasSnapshotDriftAuditStale tell a schedd
	// that never completed a drift inventory since it started from a fresh one.
	ops.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Keep handler instrumentation in one registry: instrumenting each registry
	// would duplicate promhttp error counters when the canonical scrape merges them.
	opts := promhttp.HandlerOpts{Registry: ops}
	mux.Handle(metricsPath, promhttp.HandlerFor(prometheus.Gatherers{ops, dashboard}, opts))
	mux.Handle(metricsPath+"/fcvm", promhttp.HandlerFor(dashboard, opts))
}
