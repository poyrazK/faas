package wire

import (
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Cron delivery outcomes are a closed vocabulary shared with delayed tasks,
// plus app_error: the run reached the app and the app answered with an
// error, so it is the customer's failure, not the platform's. failed and
// dead_letter are platform failures. Keep these values stable: alerting and
// dashboards aggregate them directly.
const (
	CronDeliverySuccess    = DelayedTaskDispatchSuccess
	CronDeliveryRetry      = DelayedTaskDispatchRetry
	CronDeliveryFailed     = DelayedTaskDispatchFailed
	CronDeliveryDeadLetter = DelayedTaskDispatchDeadLetter
	CronDeliveryAppError   = "app_error"
)

type cronDeliveryMetrics struct {
	deliveryTotal      *prometheus.CounterVec
	scheduleLagSeconds prometheus.Histogram
}

func newCronDeliveryMetrics(prefix string) *cronDeliveryMetrics {
	m := &cronDeliveryMetrics{
		deliveryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_cron_delivery_total",
			Help: "Count of HTTP cron delivery attempts (scheduled fires and manual runs) by outcome. Outcome is one of success, retry, app_error (the app answered with an error), failed or dead_letter (platform failures). No app or account label.",
		}, []string{"outcome"}),
		scheduleLagSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    prefix + "_cron_schedule_lag_seconds",
			Help:    "Seconds between an HTTP cron run's scheduled time (or a manual run's request time) and its first delivery claim.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 900},
		}),
	}
	for _, outcome := range []string{
		CronDeliverySuccess,
		CronDeliveryRetry,
		CronDeliveryFailed,
		CronDeliveryDeadLetter,
		CronDeliveryAppError,
	} {
		m.deliveryTotal.WithLabelValues(outcome)
	}
	return m
}

// ObserveCronDelivery records one settled HTTP cron delivery attempt. Unknown
// outcomes are ignored so error text cannot leak into Prometheus labels.
func (m *OpsMetrics) ObserveCronDelivery(outcome string) {
	if m == nil || m.cronDelivery == nil {
		return
	}
	switch outcome {
	case CronDeliverySuccess,
		CronDeliveryRetry,
		CronDeliveryFailed,
		CronDeliveryDeadLetter,
		CronDeliveryAppError:
		m.cronDelivery.deliveryTotal.WithLabelValues(outcome).Inc()
	}
}

// ObserveCronScheduleLag records lag for the first claim only. Negative and
// non-finite values are discarded instead of corrupting the histogram.
func (m *OpsMetrics) ObserveCronScheduleLag(lag time.Duration) {
	if m == nil || m.cronDelivery == nil {
		return
	}
	seconds := lag.Seconds()
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return
	}
	m.cronDelivery.scheduleLagSeconds.Observe(seconds)
}
