package wire

import "github.com/prometheus/client_golang/prometheus"

type eventDeliveryMetrics struct {
	deferrals *prometheus.CounterVec
	waiting   prometheus.Gauge
	oldest    prometheus.Gauge
	outcomes  *prometheus.CounterVec
}

// EventRoutingOutcome labels schedd_event_routing_outcomes_total. A routing
// pass that fails every claim still completes, so the sweep counter alone
// cannot tell routing that runs from routing that delivers.
const (
	EventRoutingRouted     = "routed"      // recipient delivered, admitted or matched nothing
	EventRoutingWaiting    = "waiting"     // recipient waits for delivery capacity
	EventRoutingRetry      = "retry"       // recipient failed and will be routed again
	EventRoutingFailed     = "failed"      // recipient failed for good
	EventRoutingClaimError = "claim_error" // the pass could not claim routing work
)

func newEventDeliveryMetrics(prefix string) *eventDeliveryMetrics {
	m := &eventDeliveryMetrics{
		deferrals: prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_event_delivery_capacity_deferrals_total", Help: "Committed routing deferrals due to live delivery capacity."}, []string{"scope"}),
		waiting:   prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_event_delivery_capacity_waiting", Help: "Captured event recipients currently waiting for delivery capacity."}),
		oldest:    prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_event_routing_oldest_pending_seconds", Help: "Age since acceptance of the oldest unsettled captured event recipient; zero when empty."}),
		outcomes:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: prefix + "_event_routing_outcomes_total", Help: "Event routing results: each recipient routed, waiting for capacity, retried or failed, and each claim the pass could not make."}, []string{"outcome"}),
	}
	for _, scope := range []string{"consumer", "app", "account"} {
		m.deferrals.WithLabelValues(scope).Add(0)
	}
	for _, outcome := range []string{EventRoutingRouted, EventRoutingWaiting, EventRoutingRetry, EventRoutingFailed, EventRoutingClaimError} {
		m.outcomes.WithLabelValues(outcome).Add(0)
	}
	return m
}

// ObserveEventRoutingOutcome counts one routing result; unknown outcomes are
// dropped so the label set stays closed.
func (m *OpsMetrics) ObserveEventRoutingOutcome(outcome string) {
	if m == nil || m.eventDelivery == nil {
		return
	}
	switch outcome {
	case EventRoutingRouted, EventRoutingWaiting, EventRoutingRetry, EventRoutingFailed, EventRoutingClaimError:
		m.eventDelivery.outcomes.WithLabelValues(outcome).Inc()
	}
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
