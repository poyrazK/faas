package wire

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Recovery metrics aggregate eligible owned apps. Customer identifiers and
// environment names never enter the label set; missing observations stay unknown.
type workerRecoveryMetrics struct {
	apps       *prometheus.GaugeVec
	replicas   *prometheus.GaugeVec
	observedAt prometheus.Gauge
}

func newWorkerRecoveryMetrics(prefix string) *workerRecoveryMetrics {
	m := &workerRecoveryMetrics{
		apps:       prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_worker_recovery_apps", Help: "Eligible owned worker apps by diagnostic condition; unknown includes missing or stale pool observations."}, []string{"condition"}),
		replicas:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_worker_recovery_replicas", Help: "Fleet totals from fresh scheduler pool observations; missing is summed per pool so surplus in another environment cannot hide a deficit."}, []string{"state"}),
		observedAt: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_worker_recovery_observed_timestamp_seconds", Help: "Last successful database collection of worker recovery diagnostics; zero means no successful collection."}),
	}
	for _, condition := range []string{"eligible", "unknown", "overdue"} {
		m.apps.WithLabelValues(condition)
	}
	for _, state := range []string{"desired", "running", "starting", "draining", "missing"} {
		m.replicas.WithLabelValues(state)
	}
	return m
}

// SetWorkerRecoverySummary replaces the whole owner-scoped snapshot, including
// zeros after recovery, parking, account holds, or ownership changes. Called
// only after a successful database read; failures leave its timestamp stale.
func (m *OpsMetrics) SetWorkerRecoverySummary(apps, unknown, overdue, desired, running, starting, draining, missing int64, now time.Time) {
	if m == nil || m.workerRecovery == nil {
		return
	}
	r := m.workerRecovery
	for condition, value := range map[string]int64{"eligible": apps, "unknown": unknown, "overdue": overdue} {
		r.apps.WithLabelValues(condition).Set(float64(max(0, value)))
	}
	for state, value := range map[string]int64{"desired": desired, "running": running, "starting": starting, "draining": draining, "missing": missing} {
		r.replicas.WithLabelValues(state).Set(float64(max(0, value)))
	}
	r.observedAt.Set(float64(now.Unix()))
}
