package s3gateway

import (
	"time"

	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/prometheus/client_golang/prometheus"
)

func registerRecoveryCleanupMetrics(h *DiskHandler, registry *prometheus.Registry) {
	for name, read := range map[string]func(objectnode.RecoveryCleanupStatus) float64{
		"steps_total":            func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.Steps) },
		"failures_total":         func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.Failures) },
		"examined_total":         func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.Examined) },
		"protected_total":        func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.Protected) },
		"stale_total":            func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.Stale) },
		"receipts_removed_total": func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.ReceiptsRemoved) },
		"uploads_removed_total":  func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.UploadsRemoved) },
		"index_rebuilds_total":   func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.IndexRebuilds) },
		"index_replayed_total":   func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.IndexReplayed) },
	} {
		registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "faas_object_node_recovery_cleanup_" + name, Help: "Process-local indexed recovery cleanup observations."}, func() float64 {
			s, _ := h.config.Store.RecoveryCleanupStatus()
			return read(s)
		}))
	}
	for name, read := range map[string]func(objectnode.RecoveryCleanupStatus) float64{
		"enabled":    func(s objectnode.RecoveryCleanupStatus) float64 { return recoveryCleanupBool(s.Enabled) },
		"ready":      func(s objectnode.RecoveryCleanupStatus) float64 { return recoveryCleanupBool(s.Ready) },
		"deferred":   func(s objectnode.RecoveryCleanupStatus) float64 { return recoveryCleanupBool(s.Deferred) },
		"candidates": func(s objectnode.RecoveryCleanupStatus) float64 { return float64(s.Candidates) },
		"due_age_seconds": func(s objectnode.RecoveryCleanupStatus) float64 {
			if s.NextDueAt.IsZero() {
				return 0
			}
			return max(0, time.Since(s.NextDueAt).Seconds())
		},
		"last_success_timestamp_seconds": func(s objectnode.RecoveryCleanupStatus) float64 {
			if s.LastSuccessAt.IsZero() {
				return 0
			}
			return float64(s.LastSuccessAt.Unix())
		},
	} {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "faas_object_node_recovery_cleanup_" + name, Help: "Cached cleanup scheduling state without scrape-time disk I/O; candidates include protected live proofs."}, func() float64 {
			s, _ := h.config.Store.RecoveryCleanupStatus()
			return read(s)
		}))
	}
}

func recoveryCleanupBool(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
