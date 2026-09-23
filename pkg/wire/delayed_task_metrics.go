package wire

import (
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Delayed-task dispatch outcomes are intentionally a closed vocabulary. Keep
// these values stable: alerting and dashboards aggregate them directly.
const (
	DelayedTaskDispatchSuccess    = "success"
	DelayedTaskDispatchRetry      = "retry"
	DelayedTaskDispatchFailed     = "failed"
	DelayedTaskDispatchDeadLetter = "dead_letter"
)

type delayedTaskMetrics struct {
	dispatchTotal      *prometheus.CounterVec
	scheduleLagSeconds prometheus.Histogram
}

func newDelayedTaskMetrics(prefix string) *delayedTaskMetrics {
	m := &delayedTaskMetrics{
		dispatchTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_delayed_task_dispatch_total",
			Help: "Count of delayed-task dispatch attempts by outcome. Outcome is one of success, retry, failed, or dead_letter.",
		}, []string{"outcome"}),
		scheduleLagSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    prefix + "_delayed_task_schedule_lag_seconds",
			Help:    "Seconds between a delayed task's requested execution time and its first dispatch claim.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 900},
		}),
	}
	for _, outcome := range []string{
		DelayedTaskDispatchSuccess,
		DelayedTaskDispatchRetry,
		DelayedTaskDispatchFailed,
		DelayedTaskDispatchDeadLetter,
	} {
		m.dispatchTotal.WithLabelValues(outcome)
	}
	return m
}

// ObserveDelayedTaskDispatch records one completed dispatch attempt. Unknown
// outcomes are ignored so error text or future unbounded values cannot leak
// into Prometheus labels.
func (m *OpsMetrics) ObserveDelayedTaskDispatch(outcome string) {
	if m == nil || m.delayedTasks == nil {
		return
	}
	switch outcome {
	case DelayedTaskDispatchSuccess,
		DelayedTaskDispatchRetry,
		DelayedTaskDispatchFailed,
		DelayedTaskDispatchDeadLetter:
		m.delayedTasks.dispatchTotal.WithLabelValues(outcome).Inc()
	}
}

// ObserveDelayedTaskScheduleLag records lag for the first claim only. Negative
// and non-finite values are discarded instead of corrupting the histogram.
func (m *OpsMetrics) ObserveDelayedTaskScheduleLag(lag time.Duration) {
	if m == nil || m.delayedTasks == nil {
		return
	}
	seconds := lag.Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	m.delayedTasks.scheduleLagSeconds.Observe(seconds)
}
