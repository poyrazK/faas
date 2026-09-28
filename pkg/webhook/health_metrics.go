package webhook

import (
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

// DeliveryHealthMetrics exposes fleet-wide signals only. Customer and webhook
// identifiers never appear as metric labels.
type DeliveryHealthMetrics struct {
	overdueSeconds    prometheus.Gauge
	heldDueCount      prometheus.Gauge
	heldDueSeconds    prometheus.Gauge
	deadTotal         prometheus.Counter
	pollSuccess       prometheus.Gauge
	retentionSuccess  prometheus.Gauge
	retentionFailures prometheus.Counter
	prunedTotal       prometheus.Counter
	storageBytes      prometheus.Gauge
	inFlight          prometheus.Gauge
	saturated         prometheus.Gauge
}

func NewDeliveryHealthMetrics(reg prometheus.Registerer, prefix string) *DeliveryHealthMetrics {
	m := &DeliveryHealthMetrics{
		overdueSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_oldest_overdue_seconds",
			Help: "Age of the oldest claimable overdue outbound webhook delivery, or zero when no subscription has claim capacity.",
		}),
		heldDueCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_held_due_count",
			Help: "Due outbound webhook deliveries held by receiver cooldown or full subscription claim capacity.",
		}),
		heldDueSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_oldest_held_due_seconds",
			Help: "Age of the oldest due outbound webhook delivery held by receiver cooldown or full subscription claim capacity.",
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
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_inflight",
			Help: "Outbound webhook delivery workers currently running in this schedd process.",
		}),
		saturated: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_webhook_delivery_saturated",
			Help: "One when all outbound webhook dispatch slots are reserved or running, zero otherwise.",
		}),
	}
	reg.MustRegister(m.overdueSeconds, m.heldDueCount, m.heldDueSeconds, m.deadTotal, m.pollSuccess, m.retentionSuccess,
		m.retentionFailures, m.prunedTotal, m.storageBytes, m.inFlight, m.saturated)
	return m
}

func (m *DeliveryHealthMetrics) setFleetQueueHealth(now time.Time, health state.AppWebhookFleetQueueHealth) {
	if m == nil {
		return
	}
	m.overdueSeconds.Set(webhookDeliveryAgeSeconds(now, health.OldestClaimableAt))
	m.heldDueCount.Set(float64(health.HeldDueCount))
	m.heldDueSeconds.Set(webhookDeliveryAgeSeconds(now, health.OldestHeldAt))
	m.pollSuccess.Set(1)
}

func webhookDeliveryAgeSeconds(now time.Time, oldest *time.Time) float64 {
	if oldest == nil {
		return 0
	}
	return max(0, now.Sub(*oldest).Seconds())
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

func (m *DeliveryHealthMetrics) setDispatchLoad(active, used, capacity int) {
	if m == nil {
		return
	}
	m.inFlight.Set(float64(active))
	if capacity > 0 && used >= capacity {
		m.saturated.Set(1)
	} else {
		m.saturated.Set(0)
	}
}
