package grace

import "github.com/prometheus/client_golang/prometheus"

type metrics struct {
	missingDeadlines     prometheus.Gauge
	expiredArtifactBytes prometheus.Gauge
	lastSuccess          prometheus.Gauge
	deleted              prometheus.Counter
	failures             *prometheus.CounterVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		missingDeadlines: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "apid_app_grace_missing_deadlines",
			Help: "Number of deleted app tombstones quarantined because their purge deadline is missing.",
		}),
		expiredArtifactBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "apid_app_grace_expired_artifact_bytes",
			Help: "Known physical artifact bytes still retained for apps whose deletion grace has expired.",
		}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "apid_app_grace_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last completed app deletion grace scan.",
		}),
		deleted: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "apid_app_grace_deleted_total",
			Help: "Total app tombstones permanently removed after artifact cleanup.",
		}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "apid_app_grace_failures_total",
			Help: "Total app deletion grace failures by stage.",
		}, []string{"stage"}),
	}
	if reg != nil {
		reg.MustRegister(m.missingDeadlines, m.expiredArtifactBytes, m.lastSuccess, m.deleted, m.failures)
	}
	return m
}

func (m *metrics) failure(stage string) {
	if m != nil {
		m.failures.WithLabelValues(stage).Inc()
	}
}
