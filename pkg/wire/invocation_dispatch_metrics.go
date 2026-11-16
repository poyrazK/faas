package wire

import "github.com/prometheus/client_golang/prometheus"

// invocationDispatchMetrics expose schedd's drain dispatch pool (the slots
// every tenant's async invocations, event deliveries and inbound webhooks
// share, FAAS_SCHEDD_INVOCATION_DISPATCH_CONCURRENCY). A pool held full means
// that work waits for a slot, not for the app.
type invocationDispatchMetrics struct {
	used prometheus.Gauge
	size prometheus.Gauge
}

func newInvocationDispatchMetrics(prefix string) *invocationDispatchMetrics {
	return &invocationDispatchMetrics{
		used: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_invocation_dispatch_slots_used", Help: "Drain dispatch slots delivering an invocation now."}),
		size: prometheus.NewGauge(prometheus.GaugeOpts{Name: prefix + "_invocation_dispatch_slots", Help: "Drain dispatch slots in this schedd's pool."}),
	}
}

// SetInvocationDispatchSlots records the drain pool's occupancy.
func (m *OpsMetrics) SetInvocationDispatchSlots(used, size int) {
	if m == nil || m.invocationDispatch == nil {
		return
	}
	m.invocationDispatch.used.Set(float64(max(0, used)))
	m.invocationDispatch.size.Set(float64(max(0, size)))
}
