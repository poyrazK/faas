package wire

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// JobFlexibleMetrics measures the accepted-window scheduling contract. Labels
// use closed vocabularies and never include account, job, or run IDs.
type JobFlexibleMetrics struct {
	expired      prometheus.Counter
	started      prometheus.Counter
	deferred     *prometheus.CounterVec
	queueSeconds prometheus.Histogram
}

func NewJobFlexibleMetrics(reg prometheus.Registerer, prefix string) *JobFlexibleMetrics {
	metrics := &JobFlexibleMetrics{
		expired:      prometheus.NewCounter(prometheus.CounterOpts{Name: prefix + "_job_flexible_expired_runs_total", Help: "Flexible job runs with unstarted tasks expired at latest_start_at."}),
		started:      prometheus.NewCounter(prometheus.CounterOpts{Name: prefix + "_job_flexible_started_tasks_total", Help: "Flexible job tasks admitted inside their start window."}),
		deferred:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_job_flexible_deferred_tasks_total", Help: "Flexible job tasks deferred by scheduler admission."}, []string{"reason"}),
		queueSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{Name: prefix + "_job_flexible_queue_seconds", Help: "Seconds from flexible run creation to task admission.", Buckets: []float64{1, 5, 15, 60, 300, 900, 3600, 21600, 86400}}),
	}
	reg.MustRegister(metrics.expired, metrics.started, metrics.deferred, metrics.queueSeconds)
	return metrics
}

func (m *JobFlexibleMetrics) ExpiredRun() {
	if m != nil {
		m.expired.Inc()
	}
}
func (m *JobFlexibleMetrics) StartedTask(createdAt time.Time) {
	if m == nil {
		return
	}
	m.started.Inc()
	m.queueSeconds.Observe(max(0, time.Since(createdAt).Seconds()))
}
func (m *JobFlexibleMetrics) DeferredTask(reason string) {
	if m == nil {
		return
	}
	switch reason {
	case "account_capacity", "wake_error":
	default:
		reason = "wake_error"
	}
	m.deferred.WithLabelValues(reason).Inc()
}
