// Package routejobs runs and instruments apid's background route workers:
// the production route monitor and the automatic route requirement checks
// (ADR-20261024154324).
package routejobs

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Worker names label queue failures.
const (
	WorkerMonitor = "monitor"
	WorkerCheck   = "check"
)

// Monitor evaluation outcomes.
const (
	MonitorEvaluated = "evaluated"
	MonitorSkipped   = "skipped"
	MonitorFailed    = "failed"
)

// Automatic route check outcomes.
const (
	CheckCompleted = "completed"
	CheckSkipped   = "skipped"
	CheckRetried   = "retried"
)

// Metrics counts what the route workers did. Its methods are nil-receiver
// safe, so workers built without a registry record nothing.
type Metrics struct {
	registry           *prometheus.Registry
	monitorEvaluations *prometheus.CounterVec
	monitorLag         prometheus.Gauge
	monitorLastDrain   prometheus.Gauge
	checks             *prometheus.CounterVec
	queueUnavailable   *prometheus.CounterVec
}

// NewMetrics registers the route worker metrics on registry.
func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	m := &Metrics{registry: registry}
	m.monitorEvaluations = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_route_monitor_evaluations_total",
		Help: "Production route monitor evaluations by outcome: evaluated, skipped (another replica holds the monitor or it is no longer due) or failed (deferred for retry).",
	}, []string{"outcome"}))
	m.monitorLag = register(registry, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_route_monitor_lag_seconds",
		Help: "How long the oldest due production route monitor had waited when this replica's latest drain started; zero when none was due.",
	}))
	m.monitorLastDrain = register(registry, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_route_monitor_last_drain_timestamp_seconds",
		Help: "Unix time this replica last finished draining due production route monitors.",
	}))
	m.checks = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_route_checks_total",
		Help: "Automatic route requirement checks by outcome: completed, skipped (superseded or already settled) or retried (failed and rescheduled).",
	}, []string{"outcome"}))
	m.queueUnavailable = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_route_worker_queue_unavailable_total",
		Help: "Route worker drains that could not read their queue, by worker: monitor or check.",
	}, []string{"worker"}))
	for _, outcome := range []string{MonitorEvaluated, MonitorSkipped, MonitorFailed} {
		m.monitorEvaluations.WithLabelValues(outcome)
	}
	for _, outcome := range []string{CheckCompleted, CheckSkipped, CheckRetried} {
		m.checks.WithLabelValues(outcome)
	}
	for _, worker := range []string{WorkerMonitor, WorkerCheck} {
		m.queueUnavailable.WithLabelValues(worker)
	}
	return m
}

// Registry reports the registry the metrics live in.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *Metrics) observeMonitor(outcome string) {
	if m != nil {
		m.monitorEvaluations.WithLabelValues(outcome).Inc()
	}
}

func (m *Metrics) setMonitorLag(d time.Duration) {
	if m != nil {
		m.monitorLag.Set(max(0, d.Seconds()))
	}
}

func (m *Metrics) monitorDrained(at time.Time) {
	if m != nil {
		m.monitorLastDrain.Set(float64(at.Unix()))
	}
}

// ObserveCheck records one automatic route check outcome.
func (m *Metrics) ObserveCheck(outcome string) {
	if m == nil {
		return
	}
	switch outcome {
	case CheckCompleted, CheckSkipped, CheckRetried:
		m.checks.WithLabelValues(outcome).Inc()
	}
}

// QueueUnavailable records a drain that could not read its queue.
func (m *Metrics) QueueUnavailable(worker string) {
	if m == nil {
		return
	}
	switch worker {
	case WorkerMonitor, WorkerCheck:
		m.queueUnavailable.WithLabelValues(worker).Inc()
	}
}

func register[T prometheus.Collector](registry *prometheus.Registry, c T) T {
	err := registry.Register(c)
	if err == nil {
		return c
	}
	var already prometheus.AlreadyRegisteredError
	if errors.As(err, &already) {
		if existing, ok := already.ExistingCollector.(T); ok {
			return existing
		}
	}
	panic(err)
}
