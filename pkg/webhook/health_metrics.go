package webhook

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DeliveryHealthMetrics exposes fleet-wide signals only. Customer and webhook
// identifiers never appear as metric labels.
type DeliveryHealthMetrics struct {
	overdueSeconds prometheus.Gauge
	deadTotal      prometheus.Counter
	pollSuccess    prometheus.Gauge
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
	}
	reg.MustRegister(m.overdueSeconds, m.deadTotal, m.pollSuccess)
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
