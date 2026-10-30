package agentd

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// Metrics is agentd's Prometheus surface for runs and deliveries
// (ADR-20261027090010 §12 additions). Labels are bounded: run states, start
// stages, gRPC codes, destinations, delivery states and attempt results.
// Nothing a guest reports becomes a label.
type Metrics struct {
	runs             *prometheus.CounterVec
	requeues         *prometheus.CounterVec
	startSeconds     *prometheus.HistogramVec
	runSeconds       prometheus.Histogram
	queueDepth       prometheus.Gauge
	queueOldest      prometheus.Gauge
	deliveries       *prometheus.CounterVec
	deliveryAttempts *prometheus.CounterVec
	attemptSeconds   *prometheus.HistogramVec
}

// Start stages of agent_run_start_seconds.
const (
	stageQueue = "queue" // queued until a replica claims it
	stageInput = "input" // the checkout prepared on the input drive
	stagePlace = "place" // schedd's admission on the agent pool
	stageBoot  = "boot"  // vmmd's boot, as schedd reports it
)

// NewMetrics registers agentd's metrics on reg.
func NewMetrics(reg prometheus.Registerer) (*Metrics, error) {
	if reg == nil {
		return nil, errors.New("agentd: nil prometheus registerer")
	}
	m := &Metrics{
		runs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_runs_total",
			Help: "Agent runs this replica finished, by terminal state (succeeded, failed, timed_out, out_of_memory, cancelled).",
		}, []string{"outcome"}),
		requeues: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_run_requeues_total",
			Help: "Claimed agent runs queued again before reaching a VM, by the gRPC code of the cause.",
		}, []string{"cause"}),
		startSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "agent_run_start_seconds",
			Help:    "Time an agent run spent in one start stage (queue, input, place, boot).",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 900},
		}, []string{"stage"}),
		runSeconds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "agent_run_duration_seconds",
			Help:    "Time from an agent run's placement on the agent pool to its terminal state.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 900, 1800, 3600},
		}),
		queueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "agent_run_queue_depth",
			Help: "Queued agent runs across every account, as this replica last read them.",
		}),
		queueOldest: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "agent_run_queue_oldest_seconds",
			Help: "How long the oldest queued agent run has waited, as this replica last read it; 0 when none is queued.",
		}),
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
	for _, c := range []prometheus.Collector{m.runs, m.requeues, m.startSeconds, m.runSeconds, m.queueDepth, m.queueOldest,
		m.deliveries, m.deliveryAttempts, m.attemptSeconds} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// stage records the time a run spent in one start stage.
func (m *Metrics) stage(name string, took time.Duration) {
	if m == nil || took < 0 {
		return
	}
	m.startSeconds.WithLabelValues(name).Observe(took.Seconds())
}

// finished counts a run that reached a terminal state.
func (m *Metrics) finished(state string) {
	if m == nil {
		return
	}
	switch state {
	case agentstore.StateSucceeded, agentstore.StateFailed, agentstore.StateTimedOut, agentstore.StateOutOfMemory, agentstore.StateCancelled:
		m.runs.WithLabelValues(state).Inc()
	}
}

// ran records how long a placed run took to reach its terminal state.
func (m *Metrics) ran(took time.Duration) {
	if m == nil {
		return
	}
	m.runSeconds.Observe(took.Seconds())
}

// requeued counts a claimed run that went back to the queue.
func (m *Metrics) requeued(cause string) {
	if m == nil {
		return
	}
	m.requeues.WithLabelValues(cause).Inc()
}

// queue records the queue's depth and the oldest queued run's wait.
func (m *Metrics) queue(depth int, oldest time.Duration) {
	if m == nil {
		return
	}
	m.queueDepth.Set(float64(depth))
	m.queueOldest.Set(oldest.Seconds())
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
