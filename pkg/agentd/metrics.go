package agentd

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Metrics is agentd's Prometheus surface for deliveries (ADR-20261027090010 §12
// additions). Labels are bounded: destinations, delivery states and attempt
// results.
type Metrics struct {
	deliveries       *prometheus.CounterVec
	deliveryAttempts *prometheus.CounterVec
	attemptSeconds   *prometheus.HistogramVec
}

// NewMetrics registers agentd's metrics on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, errors.New("agentd: nil prometheus registerer")
	}
	m := &Metrics{
		deliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_deliveries_total",
			Help: "Agent run deliveries that ended, by destination and final state.",
		}, []string{"destination", "outcome"}),
		deliveryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_delivery_attempts_total",
			Help: "Agent run delivery attempts, by destination and result (delivered, retry, rejected, unknown).",
		}, []string{"destination", "result"}),
		attemptSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agent_delivery_attempt_seconds",
			Help:    "Duration of one agent run delivery attempt, by destination.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 20, 40},
		}, []string{"destination"}),
	}
	for _, c := range []prometheus.Collector{m.deliveries, m.deliveryAttempts, m.attemptSeconds} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// attempted records one delivery attempt and, when it ended the delivery,
// its final state.
func (m *Metrics) attempted(destination string, result agentstore.DeliveryResult, state string, took time.Duration) {
	if m == nil {
		return
	}
	m.deliveryAttempts.WithLabelValues(destination, attemptResult(result.Outcome)).Inc()
	m.attemptSeconds.WithLabelValues(destination).Observe(took.Seconds())
	m.ended(agentstore.DeliveryEnd{Destination: destination, State: state})
}

// ended counts a delivery that reached a final state.
func (m *Metrics) ended(e agentstore.DeliveryEnd) {
	if m == nil {
		return
	}
	switch e.State {
	case api.AgentDeliveryDelivered, api.AgentDeliveryFailed, api.AgentDeliveryOutcomeUnknown, api.AgentDeliverySuperseded, api.AgentDeliveryCancelled:
		m.deliveries.WithLabelValues(e.Destination, e.State).Inc()
	}
}

func attemptResult(o agentstore.DeliveryOutcome) string {
	switch o {
	case agentstore.DeliveryDelivered:
		return "delivered"
	case agentstore.DeliveryRetry:
		return "retry"
	case agentstore.DeliveryRejected:
		return "rejected"
	default:
		return "unknown"
	}
}
