package s3gateway

import (
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

func registerCompactionMetrics(h *DiskHandler, registry *prometheus.Registry) {
	for name, read := range map[string]func(objectnode.CompactionStatus) float64{
		"steps_total":          func(s objectnode.CompactionStatus) float64 { return float64(s.Steps) },
		"failures_total":       func(s objectnode.CompactionStatus) float64 { return float64(s.Failures) },
		"cycles_total":         func(s objectnode.CompactionStatus) float64 { return float64(s.Cycles) },
		"records_pruned_total": func(s objectnode.CompactionStatus) float64 { return float64(s.RecordsPruned) },
		"blobs_pruned_total":   func(s objectnode.CompactionStatus) float64 { return float64(s.BlobsPruned) },
		"bytes_unlinked_total": func(s objectnode.CompactionStatus) float64 { return float64(s.BytesUnlinked) },
	} {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "faas_object_node_retention_compaction_" + name, Help: "Process-local automatic journal cleanup; unlinked bytes do not measure freed disk blocks."}, func() float64 {
			s, err := h.config.Store.CompactionStatus()
			if err != nil {
				return 0
			}
			return read(s)
		}))
	}
	for name, read := range map[string]func(objectnode.CompactionStatus) float64{
		"active": func(s objectnode.CompactionStatus) float64 {
			if s.Active {
				return 1
			}
			return 0
		},
		"pinned": func(s objectnode.CompactionStatus) float64 {
			if s.Pinned {
				return 1
			}
			return 0
		},
		"deferred": func(s objectnode.CompactionStatus) float64 {
			if s.Deferred {
				return 1
			}
			return 0
		},
		"last_success_timestamp_seconds": func(s objectnode.CompactionStatus) float64 {
			if s.LastSuccessAt.IsZero() {
				return 0
			}
			return float64(s.LastSuccessAt.Unix())
		},
	} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_retention_compaction_" + name, Help: "Cached automatic journal maintenance state without scrape-time disk I/O."}, func() float64 {
			s, err := h.config.Store.CompactionStatus()
			if err != nil {
				return 0
			}
			return read(s)
		}))
	}
}
