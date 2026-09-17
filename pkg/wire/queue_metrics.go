package wire

import (
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type queueMetrics struct {
	depth      *prometheus.GaugeVec
	inFlight   *prometheus.GaugeVec
	oldestAge  *prometheus.GaugeVec
	deadLetter *prometheus.GaugeVec
}

func newQueueMetrics(prefix string) *queueMetrics {
	return &queueMetrics{
		depth:      prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_depth", Help: "Current durable queue depth, labelled by admitted app."}, []string{"app"}),
		inFlight:   prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_in_flight", Help: "Current durable queue invocations with a live worker lease, labelled by admitted app."}, []string{"app"}),
		oldestAge:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_oldest_age_seconds", Help: "Age in seconds of the oldest pending durable queue invocation, labelled by admitted app; zero means no pending work."}, []string{"app"}),
		deadLetter: prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: prefix + "_queue_dead_letter", Help: "Current durable queue dead-letter count, labelled by admitted app."}, []string{"app"}),
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

// SetQueueState records the durable queue projection for an app.
func (m *OpsMetrics) SetQueueState(app string, depth, inFlight, deadLetter int, oldestPendingAt, now time.Time) {
	if m == nil || m.queue == nil {
		return
	}
	m.queue.set(m.appLabel(app), depth, inFlight, deadLetter, oldestPendingAt, now)
}
