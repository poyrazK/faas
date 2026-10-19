package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

type buildWorkerSnapshot struct {
	started, attempt, completed time.Time
	enabled, inflight           bool
}

// buildWorkerMetrics describes the durable poller only. Notification workers
// can make progress independently and cannot renew these timestamps.
type buildWorkerMetrics struct {
	mu                      sync.RWMutex
	snapshot                buildWorkerSnapshot
	descs                   []*prometheus.Desc
	iterations              *prometheus.CounterVec
	interval, busyAllowance time.Duration
	now                     func() time.Time
}

func newBuildWorkerMetrics(reg prometheus.Registerer, interval, busyAllowance time.Duration) *buildWorkerMetrics {
	m := &buildWorkerMetrics{
		snapshot: buildWorkerSnapshot{started: time.Now().UTC(), enabled: true},
		interval: interval, busyAllowance: busyAllowance, now: time.Now,
		iterations: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "builderd_build_worker_iterations_total", Help: "Completed durable poll iterations by fixed response outcome; completed means ProcessNext returned nil, not necessarily a VM start."}, []string{"outcome"}),
	}
	for _, item := range []struct{ name, help string }{
		{"builderd_build_worker_started_timestamp_seconds", "Poller initialization time in Unix seconds; used before its first completion."},
		{"builderd_build_worker_last_attempt_timestamp_seconds", "Start of the latest poll iteration in Unix seconds; zero before first attempt."},
		{"builderd_build_worker_last_completed_timestamp_seconds", "Completion of the latest poll iteration in Unix seconds, including idle and failed attempts."},
		{"builderd_build_worker_enabled", "Whether durable polling is expected. Zero after parent cancellation or intentional drain; unexpected loop exit remains one."},
		{"builderd_build_worker_inflight", "Whether the durable poller is inside ProcessNext, including preparation, cache completion and the full VM build."},
		{"builderd_build_worker_interval_seconds", "Effective configured durable poll interval in seconds."},
		{"builderd_build_worker_busy_allowance_seconds", "Effective running-build recovery threshold used as the observation allowance for a full in-flight poll iteration; does not enforce a deadline."},
	} {
		m.descs = append(m.descs, prometheus.NewDesc(item.name, item.help, nil, nil))
	}
	for _, outcome := range []string{"completed", "not_claimed", "no_slot", "error", "canceled", "draining"} {
		m.iterations.WithLabelValues(outcome)
	}
	reg.MustRegister(m)
	return m
}

func (m *buildWorkerMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range m.descs {
		ch <- desc
	}
	m.iterations.Describe(ch)
}

func (m *buildWorkerMetrics) Collect(ch chan<- prometheus.Metric) {
	m.mu.RLock()
	s := m.snapshot
	m.mu.RUnlock()
	unix := func(t time.Time) float64 {
		if t.IsZero() {
			return 0
		}
		return float64(t.Unix()) + float64(t.Nanosecond())/float64(time.Second)
	}
	boolValue := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	values := []float64{unix(s.started), unix(s.attempt), unix(s.completed), boolValue(s.enabled), boolValue(s.inflight), m.interval.Seconds(), m.busyAllowance.Seconds()}
	for i, value := range values {
		ch <- prometheus.MustNewConstMetric(m.descs[i], prometheus.GaugeValue, value)
	}
	m.iterations.Collect(ch)
}

func (m *buildWorkerMetrics) begin() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot.attempt = m.now().UTC()
	m.snapshot.inflight = true
}

func (m *buildWorkerMetrics) finish(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.snapshot.completed = m.now().UTC()
	m.snapshot.inflight = false
	m.mu.Unlock()
	outcome := "error"
	switch {
	case err == nil:
		outcome = "completed"
	case errors.Is(err, builderd.ErrDraining):
		outcome = "draining"
	case errors.Is(err, context.Canceled):
		outcome = "canceled"
	case errors.Is(err, builderd.ErrNoSlot):
		outcome = "no_slot"
	case errors.Is(err, state.ErrNotFound) && !isVanishedRowErr(err):
		outcome = "not_claimed"
	}
	m.iterations.WithLabelValues(outcome).Inc()
}

func (m *buildWorkerMetrics) stop(normal bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot.inflight = false
	if normal {
		m.snapshot.enabled = false
	}
}
