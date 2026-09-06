package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// newMetricsMux combines vmmd's registries without adding identically named
// promhttp error counters to each component. Only the canonical handler owns
// those counters; component paths remain available for targeted scrapes.
func newMetricsMux(ops *wire.OpsMetrics, cbm *fcvm.ColdBootMetrics, frm *fcvm.FrameworkReadyMetrics, wpm *fcvm.WakePhaseMetrics, dsm *fcvm.DiskMetrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(metricsPath, promhttp.HandlerFor(prometheus.Gatherers{
		ops.Registry(), cbm.Registry(), frm.Registry(), wpm.Registry(), dsm.Registry(),
	}, promhttp.HandlerOpts{Registry: ops.Registry()}))
	for path, reg := range map[string]*prometheus.Registry{
		"/fallback":         cbm.Registry(),
		"/framework-warmup": frm.Registry(),
		"/wake-phase":       wpm.Registry(),
		"/disk":             dsm.Registry(),
	} {
		mux.Handle(metricsPath+path, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	}
	return mux
}
