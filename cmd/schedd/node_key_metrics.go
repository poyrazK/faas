package main

import (
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/prometheus/client_golang/prometheus"
)

// wireNodeKeyMetrics exposes the registry's bounded trust set. node_id values
// come only from durable compute_nodes rows and are therefore bounded by fleet
// membership rather than caller-controlled reports.
func wireNodeKeyMetrics(reg prometheus.Registerer, keys *sched.NodeKeyRegistry) *prometheus.GaugeVec {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "schedd_compute_node_trusted_keys",
		Help: "Currently trusted capacity-report signing keys per active compute node; expected range is one current key plus at most one rotation-overlap key.",
	}, []string{"node_id"})
	reg.MustRegister(gauge)
	keys.SetCountObserver(func(counts map[string]int) {
		gauge.Reset()
		for nodeID, count := range counts {
			gauge.WithLabelValues(nodeID).Set(float64(count))
		}
	})
	return gauge
}
