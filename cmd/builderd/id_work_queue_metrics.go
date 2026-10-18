package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	queueAccepted = iota
	queueCoalesced
	queueFull
	queueStopping
	queueInvalid
	queueOutcomeCount
)

type idWorkQueueSnapshot struct {
	waiting, active, capacity, workers int
	oldest                             time.Time
	enabled                            bool
	outcomes                           [queueOutcomeCount]uint64
}

func (q *idWorkQueue) rejectEnqueue(outcome int) bool {
	q.mu.Lock()
	q.outcomes[outcome]++
	q.mu.Unlock()
	return false
}

func (q *idWorkQueue) snapshot() idWorkQueueSnapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := idWorkQueueSnapshot{capacity: cap(q.jobs), workers: q.workers, enabled: q.ctx.Err() == nil, outcomes: q.outcomes}
	for _, queuedAt := range q.pending {
		if queuedAt.IsZero() {
			s.active++
			continue
		}
		s.waiting++
		if s.oldest.IsZero() || queuedAt.Before(s.oldest) {
			s.oldest = queuedAt
		}
	}
	return s
}

// idWorkQueueMetrics reads bounded RAM only. Copy under the queue lock, then
// release it before sending samples. Queue kinds and outcomes are closed;
// build IDs, error strings and handler results never become metric labels.
type idWorkQueueMetrics struct {
	queues  [2]*idWorkQueue
	descs   []*prometheus.Desc
	results *prometheus.Desc
}

func registerIDWorkQueueMetrics(reg prometheus.Registerer, build, cancel *idWorkQueue) {
	m := &idWorkQueueMetrics{queues: [2]*idWorkQueue{build, cancel}}
	for _, item := range []struct{ name, help string }{
		{"builderd_notification_queue_waiting", "Accepted IDs awaiting handler dispatch, including dispatch handoff; excludes active handlers."},
		{"builderd_notification_queue_capacity", "Buffered notification channel capacity; dispatch handoffs can additionally occupy up to the worker count."},
		{"builderd_notification_queue_active", "Notification handlers currently executing; does not establish a live VM or successful cancellation."},
		{"builderd_notification_queue_workers", "Configured worker count for this notification queue."},
		{"builderd_notification_queue_oldest_enqueued_timestamp_seconds", "Oldest accepted ID awaiting handler dispatch in Unix seconds; zero when no IDs are waiting."},
		{"builderd_notification_queue_enabled", "One while notification workers are expected to run; zero after their parent context is canceled."},
	} {
		m.descs = append(m.descs, prometheus.NewDesc(item.name, item.help, []string{"queue"}, nil))
	}
	m.results = prometheus.NewDesc("builderd_notification_queue_enqueues_total", "Enqueue attempts by fixed outcome; coalesced covers queued and active duplicates, full means a dropped hint requiring durable recovery.", []string{"queue", "outcome"}, nil)
	reg.MustRegister(m)
}

func (m *idWorkQueueMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.descs {
		ch <- desc
	}
	ch <- m.results
}

func (m *idWorkQueueMetrics) Collect(ch chan<- prometheus.Metric) {
	for i, kind := range []string{"build", "cancel"} {
		s := m.queues[i].snapshot()
		oldest, enabled := 0.0, 0.0
		if !s.oldest.IsZero() {
			oldest = float64(s.oldest.Unix()) + float64(s.oldest.Nanosecond())/float64(time.Second)
		}
		if s.enabled {
			enabled = 1
		}
		values := []float64{float64(s.waiting), float64(s.capacity), float64(s.active), float64(s.workers), oldest, enabled}
		for j, value := range values {
			ch <- prometheus.MustNewConstMetric(m.descs[j], prometheus.GaugeValue, value, kind)
		}
		for j, outcome := range []string{"accepted", "coalesced", "full", "stopping", "invalid"} {
			ch <- prometheus.MustNewConstMetric(m.results, prometheus.CounterValue, float64(s.outcomes[j]), kind, outcome)
		}
	}
}
