package managedpostgres

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// HealthMetrics uses only closed outcome/status labels. Database identity and
// provider error text never become Prometheus labels.
type HealthMetrics struct {
	checks     *prometheus.CounterVec
	duration   prometheus.Histogram
	databases  *prometheus.GaugeVec
	lastSweep  prometheus.Gauge
	sweeps     *prometheus.CounterVec
	enabled    prometheus.Gauge
	staleAfter prometheus.Gauge
}

func NewHealthMetrics(reg prometheus.Registerer, prefix string, policy HealthPolicy) (*HealthMetrics, error) {
	if reg == nil {
		return nil, nil
	}
	if prefix == "" {
		return nil, ErrInvalid
	}
	name := prefix + "_managed_postgres_health_"
	m := &HealthMetrics{
		checks:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: name + "checks_total", Help: "Managed PostgreSQL metadata checks by closed health outcome."}, []string{"outcome"}),
		duration:   prometheus.NewHistogram(prometheus.HistogramOpts{Name: name + "check_duration_seconds", Help: "Managed PostgreSQL metadata check duration."}),
		databases:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: name + "databases", Help: "Ready managed databases by cached provider health, across the catalog."}, []string{"status"}),
		lastSweep:  prometheus.NewGauge(prometheus.GaugeOpts{Name: name + "last_sweep_timestamp_seconds", Help: "Last successful health catalog sweep; zero before first completion."}),
		sweeps:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: name + "sweeps_total", Help: "Managed PostgreSQL health sweep results."}, []string{"outcome"}),
		enabled:    prometheus.NewGauge(prometheus.GaugeOpts{Name: name + "enabled", Help: "Whether read-only managed PostgreSQL health collection is enabled."}),
		staleAfter: prometheus.NewGauge(prometheus.GaugeOpts{Name: name + "stale_after_seconds", Help: "Configured maximum health observation age."}),
	}
	for _, collector := range []prometheus.Collector{m.checks, m.duration, m.databases, m.lastSweep, m.sweeps, m.enabled, m.staleAfter} {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	m.enabled.Set(boolGauge(policy.Enabled))
	m.staleAfter.Set(policy.StaleAfter.Seconds())
	for _, status := range []string{"healthy", "degraded", "unknown", "stale"} {
		m.databases.WithLabelValues(status).Set(0)
	}
	for _, outcome := range []string{"healthy", "degraded"} {
		m.checks.WithLabelValues(outcome).Add(0)
	}
	for _, outcome := range []string{"success", "error", "disabled"} {
		m.sweeps.WithLabelValues(outcome).Add(0)
	}
	return m, nil
}

func (m *HealthMetrics) Observe(observation HealthCollectionObservation) {
	if m == nil || (observation.Outcome != "healthy" && observation.Outcome != "degraded") {
		return
	}
	m.checks.WithLabelValues(observation.Outcome).Inc()
	m.duration.Observe(max(0, observation.Duration.Seconds()))
}

func (m *HealthMetrics) ObserveSweep(summary HealthCollectionSummary, err error) {
	if m == nil {
		return
	}
	m.enabled.Set(boolGauge(summary.Enabled))
	outcome := "success"
	if !summary.Enabled {
		outcome = "disabled"
	} else if err != nil {
		outcome = "error"
	}
	m.sweeps.WithLabelValues(outcome).Inc()
	if !summary.Enabled || err != nil || summary.CompletedAt.IsZero() {
		return
	}
	m.staleAfter.Set(float64(summary.StaleAfter / time.Second))
	m.lastSweep.Set(float64(summary.CompletedAt.Unix()))
	m.databases.WithLabelValues("healthy").Set(float64(summary.Counts.Healthy))
	m.databases.WithLabelValues("degraded").Set(float64(summary.Counts.Degraded))
	m.databases.WithLabelValues("unknown").Set(float64(summary.Counts.Unknown))
	m.databases.WithLabelValues("stale").Set(float64(summary.Counts.Stale))
}
