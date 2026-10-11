package wire

import "github.com/prometheus/client_golang/prometheus"

type eventDeliveryMetrics struct {
	deferrals *prometheus.CounterVec
	waiting   prometheus.Gauge
	oldest    prometheus.Gauge
}

func newEventDeliveryMetrics(prefix string) *eventDeliveryMetrics {
	m := &eventDeliveryMetrics{
		deferrals: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_event_delivery_capacity_deferrals_total", Help: "Committed routing deferrals due to live delivery capacity."}, []string{"scope"}),
		waiting:   prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_event_delivery_capacity_waiting", Help: "Captured event recipients currently waiting for delivery capacity."}),
		oldest:    prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_event_routing_oldest_pending_seconds", Help: "Age since acceptance of the oldest unsettled captured event recipient; zero when empty."}),
	}
	for _, scope := range []string{"consumer", "app", "account"} {
		m.deferrals.WithLabelValues(scope).Add(0)
	}
	return m
}
func (m *OpsMetrics) ObserveEventDeliveryCapacityDeferral(scope string) {
	if m == nil || m.eventDelivery == nil {
		return
	}
	switch scope {
	case "consumer", "app", "account":
		m.eventDelivery.deferrals.WithLabelValues(scope).Inc()
	}
}
func (m *OpsMetrics) SetEventRoutingHealth(waiting int64, oldest float64) {
	if m == nil || m.eventDelivery == nil {
		return
	}
	m.eventDelivery.waiting.Set(float64(max(0, waiting)))
	m.eventDelivery.oldest.Set(max(0, oldest))
}
