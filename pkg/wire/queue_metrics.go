package wire

import (
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type queueMetrics struct {
	depth             *prometheus.GaugeVec
	inFlight          *prometheus.GaugeVec
	oldestAge         *prometheus.GaugeVec
	deadLetter        *prometheus.GaugeVec
	bindingDepth      *prometheus.GaugeVec
	bindingInFlight   *prometheus.GaugeVec
	bindingLagSeconds *prometheus.GaugeVec
	bindingDeadLetter *prometheus.GaugeVec
}

func newQueueMetrics(prefix string) *queueMetrics {
	return &queueMetrics{
		depth:             prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_depth", Help: "Current durable queue depth, labelled by admitted app."}, []string{"app"}),
		inFlight:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_in_flight", Help: "Current durable queue invocations with a live worker lease, labelled by admitted app."}, []string{"app"}),
		oldestAge:         prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_oldest_age_seconds", Help: "Age in seconds of the oldest pending durable queue invocation, labelled by admitted app; zero means no pending work."}, []string{"app"}),
		deadLetter:        prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_dead_letter", Help: "Current durable queue dead-letter count, labelled by admitted app."}, []string{"app"}),
		bindingDepth:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_binding_depth", Help: "Current durable queue depth, labelled by admitted app and queue binding."}, []string{"app", "binding"}),
		bindingInFlight:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_binding_in_flight", Help: "Current durable queue invocations with a live worker lease, labelled by admitted app and queue binding."}, []string{"app", "binding"}),
		bindingLagSeconds: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_binding_lag_seconds", Help: "Age in seconds of the oldest pending invocation for a queue binding; zero means no pending work."}, []string{"app", "binding"}),
		bindingDeadLetter: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_binding_dead_letter", Help: "Current durable queue dead-letter count, labelled by admitted app and queue binding."}, []string{"app", "binding"}),
	}
}

func (q *queueMetrics) set(app string, depth, inFlight, deadLetter int, oldestPendingAt, now time.Time) {
	if q == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	if inFlight < 0 {
		inFlight = 0
	}
	if deadLetter < 0 {
		deadLetter = 0
	}
	age := 0.0
	if !oldestPendingAt.IsZero() {
		age = now.Sub(oldestPendingAt).Seconds()
		if age < 0 || math.IsNaN(age) || math.IsInf(age, 0) {
			age = 0
		}
	}
	q.depth.WithLabelValues(app).Set(float64(depth))
	q.inFlight.WithLabelValues(app).Set(float64(inFlight))
	q.oldestAge.WithLabelValues(app).Set(age)
	q.deadLetter.WithLabelValues(app).Set(float64(deadLetter))
}

func (q *queueMetrics) setBinding(app, binding string, depth, inFlight, deadLetter int, oldestPendingAt, now time.Time) {
	if q == nil {
		return
	}
	if depth < 0 {
		depth = 0
	}
	if inFlight < 0 {
		inFlight = 0
	}
	if deadLetter < 0 {
		deadLetter = 0
	}
	lag := 0.0
	if !oldestPendingAt.IsZero() {
		lag = now.Sub(oldestPendingAt).Seconds()
		if lag < 0 || math.IsNaN(lag) || math.IsInf(lag, 0) {
			lag = 0
		}
	}
	q.bindingDepth.WithLabelValues(app, binding).Set(float64(depth))
	q.bindingInFlight.WithLabelValues(app, binding).Set(float64(inFlight))
	q.bindingLagSeconds.WithLabelValues(app, binding).Set(lag)
	q.bindingDeadLetter.WithLabelValues(app, binding).Set(float64(deadLetter))
}

// SetQueueState records the durable queue projection for an app.
func (m *OpsMetrics) SetQueueState(app string, depth, inFlight, deadLetter int, oldestPendingAt, now time.Time) {
	if m == nil || m.queue == nil {
		return
	}
	m.queue.set(m.appLabel(app), depth, inFlight, deadLetter, oldestPendingAt, now)
}

// SetQueueBindingState records binding-scoped durable queue telemetry. The
// binding label is admission-bounded just like app-labelled metrics so a
// customer cannot create an unbounded Prometheus series set by adding queues.
func (m *OpsMetrics) SetQueueBindingState(app, binding string, depth, inFlight, deadLetter int, oldestPendingAt, now time.Time) {
	if m == nil || m.queue == nil {
		return
	}
	if m.queueLabels == nil {
		m.queue.setBinding(m.appLabel(app), binding, depth, inFlight, deadLetter, oldestPendingAt, now)
		return
	}
	m.queue.setBinding(m.appLabel(app), m.queueLabels.admit(binding), depth, inFlight, deadLetter, oldestPendingAt, now)
}
