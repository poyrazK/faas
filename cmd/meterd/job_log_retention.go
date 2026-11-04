package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/prometheus/client_golang/prometheus"
)

// jobLogRetentionMetrics reports the job log retention sweep
// (ADR-20261102183152), alongside the log_events retention metrics.
type jobLogRetentionMetrics struct {
	now         func() time.Time
	cleared     *prometheus.CounterVec
	lastSuccess prometheus.Gauge
	failures    prometheus.Counter
}

func newJobLogRetentionMetrics(reg *prometheus.Registry, now func() time.Time) *jobLogRetentionMetrics {
	if now == nil {
		now = time.Now
	}
	m := &jobLogRetentionMetrics{
		now: now,
		cleared: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_job_logs_retention_cleared_total",
			Help: "Job rows whose stored output plan-aware retention cleared, by table (job_runs, job_tasks, job_task_attempts).",
		}, []string{"table"}),
		lastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "meterd_job_logs_retention_last_success_timestamp_seconds",
			Help: "Latest job log retention pass that cleared every due run.",
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "meterd_job_logs_retention_failures_total",
			Help: "Job log retention passes that failed or stopped at the batch cap.",
		}),
	}
	reg.MustRegister(m.cleared, m.lastSuccess, m.failures)
	return m
}

func (m *jobLogRetentionMetrics) observe(cleared meter.JobLogsCleared, err error) {
	for table, n := range map[string]int64{
		"job_runs": cleared.Runs, "job_tasks": cleared.Tasks, "job_task_attempts": cleared.Attempts,
	} {
		if n > 0 {
			m.cleared.WithLabelValues(table).Add(float64(n))
		}
	}
	if err != nil {
		m.failures.Inc()
		return
	}
	m.lastSuccess.Set(float64(m.now().Unix()))
}
