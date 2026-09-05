package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

func registerSchedulerMetrics(mux *http.ServeMux, ops, dashboard *prometheus.Registry) {
	// Keep handler instrumentation in one registry: instrumenting each registry
	// would duplicate promhttp error counters when the canonical scrape merges them.
	opts := promhttp.HandlerOpts{Registry: ops}
	mux.Handle(metricsPath, promhttp.HandlerFor(prometheus.Gatherers{ops, dashboard}, opts))
	mux.Handle(metricsPath+"/fcvm", promhttp.HandlerFor(dashboard, opts))
}
