package s3gateway

import (
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

func registerCatalogIndexMetrics(h *DiskHandler, registry *prometheus.Registry) {
	registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_catalog_index_ready", Help: "Whether the local listing and expiry projection is ready; cached without disk I/O."}, func() float64 {
		s, _ := h.config.Store.CatalogIndexStatus()
		return recoveryCleanupBool(s.Ready)
	}))
	for name, read := range map[string]func(objectnode.CatalogIndexStatus) uint64{
		"rebuilds":        func(s objectnode.CatalogIndexStatus) uint64 { return s.Rebuilds },
		"failures":        func(s objectnode.CatalogIndexStatus) uint64 { return s.Failures },
		"list_examined":   func(s objectnode.CatalogIndexStatus) uint64 { return s.ListExamined },
		"expiry_steps":    func(s objectnode.CatalogIndexStatus) uint64 { return s.ExpirySteps },
		"expiry_examined": func(s objectnode.CatalogIndexStatus) uint64 { return s.ExpiryExamined },
		"expired":         func(s objectnode.CatalogIndexStatus) uint64 { return s.Expired },
		"parts_removed":   func(s objectnode.CatalogIndexStatus) uint64 { return s.PartsRemoved },
		"expiry_deferred": func(s objectnode.CatalogIndexStatus) uint64 { return s.ExpiryDeferred },
	} {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "faas_object_node_catalog_index_" + name + "_total", Help: "Process-local indexed catalog observations, without identity labels."}, func() float64 {
			s, _ := h.config.Store.CatalogIndexStatus()
			return float64(read(s))
		}))
	}
}
