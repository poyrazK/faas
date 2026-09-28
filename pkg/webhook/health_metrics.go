package webhook

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DeliveryHealthMetrics exposes fleet-wide signals only. Customer and webhook
// identifiers never appear as metric labels.
type DeliveryHealthMetrics struct {
	overdueSeconds    prometheus.Gauge
	deadTotal         prometheus.Counter
	pollSuccess       prometheus.Gauge
	retentionSuccess  prometheus.Gauge
	retentionFailures prometheus.Counter
	prunedTotal       prometheus.Counter
	storageBytes      prometheus.Gauge
}

func NewDeliveryHealthMetrics(reg prometheus.Registerer, prefix string) *DeliveryHealthMetrics {
	m := &DeliveryHealthMetrics{
		overdueSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_oldest_overdue_seconds",
			Help: "Age of the oldest due outbound webhook delivery, or zero when no delivery is overdue.",
		}),
		deadTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_webhook_delivery_dead_total",
			Help: "Outbound webhook deliveries newly marked dead by this dispatcher.",
		}),
		pollSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_health_poll_success",
			Help: "One when the last outbound webhook queue health poll succeeded, zero on read failure.",
		}),
		retentionSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_retention_success",
			Help: "One when the last outbound webhook retention pass and storage poll succeeded.",
		}),
		retentionFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_webhook_delivery_retention_failures_total",
			Help: "Failed outbound webhook retention passes or storage polls.",
		}),
		prunedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: prefix + "_webhook_delivery_pruned_total",
			Help: "Terminal outbound webhook deliveries removed after the retention window.",
		}),
		storageBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_storage_bytes",
			Help: "Postgres storage used by outbound webhook deliveries and attempt history, including indexes.",
		}),
	}
	reg.MustRegister(m.overdueSeconds, m.deadTotal, m.pollSuccess, m.retentionSuccess,
		m.retentionFailures, m.prunedTotal, m.storageBytes)
	return m
}

func (m *DeliveryHealthMetrics) setOldestOverdue(now time.Time, oldest *time.Time) {
	if m == nil {
		return
	}
	age := 0.0
	if oldest != nil {
		age = max(0, now.Sub(*oldest).Seconds())
	}
	m.overdueSeconds.Set(age)
	m.pollSuccess.Set(1)
}

func (m *DeliveryHealthMetrics) markPollFailed() {
	if m != nil {
		m.pollSuccess.Set(0)
	}
}

func (m *DeliveryHealthMetrics) markDead() {
	if m != nil {
		m.deadTotal.Inc()
	}
}

func (m *DeliveryHealthMetrics) markRetentionFailed() {
	if m != nil {
		m.retentionSuccess.Set(0)
		m.retentionFailures.Inc()
	}
}

func (m *DeliveryHealthMetrics) markPruned(pruned int64) {
	if m != nil {
		m.prunedTotal.Add(float64(pruned))
	}
}

func (m *DeliveryHealthMetrics) markRetentionSucceeded(bytes int64) {
	if m != nil {
		m.storageBytes.Set(float64(bytes))
		m.retentionSuccess.Set(1)
	}
}
