package s3gateway

import (
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

func registerUsageCounterMetrics(h *DiskHandler, registry *prometheus.Registry) {
	for name, read := range map[string]func(objectnode.UsageCountersStatus) float64{
		"ready":             func(s objectnode.UsageCountersStatus) float64 { return recoveryCleanupBool(s.Ready) },
		"objects":           func(s objectnode.UsageCountersStatus) float64 { return float64(s.Totals.Objects) },
		"committed_bytes":   func(s objectnode.UsageCountersStatus) float64 { return float64(s.Totals.CommittedBytes) },
		"multipart_uploads": func(s objectnode.UsageCountersStatus) float64 { return float64(s.Totals.MultipartUploads) },
		"multipart_parts":   func(s objectnode.UsageCountersStatus) float64 { return float64(s.Totals.MultipartParts) },
		"multipart_bytes":   func(s objectnode.UsageCountersStatus) float64 { return float64(s.Totals.MultipartBytes) },
		"last_audit_timestamp_seconds": func(s objectnode.UsageCountersStatus) float64 {
			if s.AuditedAt.IsZero() {
				return 0
			}
			return float64(s.AuditedAt.Unix())
		},
	} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_usage_counters_" + name, Help: "Cached logical usage projection; totals exclude reservations and physical disk overhead."}, func() float64 {
			s, _ := h.config.Store.UsageCountersStatus()
			return read(s)
		}))
	}
	for name, read := range map[string]func(objectnode.UsageCountersStatus) uint64{
		"queries":        func(s objectnode.UsageCountersStatus) uint64 { return s.Queries },
		"drift_failures": func(s objectnode.UsageCountersStatus) uint64 { return s.AuditFailures },
	} {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "faas_object_node_usage_counters_" + name + "_total", Help: "Process-local usage counter observations without identity labels."}, func() float64 {
			s, _ := h.config.Store.UsageCountersStatus()
			return float64(read(s))
		}))
	}
}
