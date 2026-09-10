package main

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// statusMetrics contains only closed-set labels. Public event IDs, titles,
// actors, and components never become labels, which keeps operator activity
// from growing the Prometheus series count.
type statusMetrics struct {
	registry          *prometheus.Registry
	evaluations       *prometheus.CounterVec
	rollupLastSuccess prometheus.Gauge
	mutations         *prometheus.CounterVec
}

func newStatusMetrics(registry *prometheus.Registry, prefix string) *statusMetrics {
	m := &statusMetrics{
		registry: registry,
		evaluations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_status_evaluations_total",
			Help: "Status evaluator runs by bounded outcome.",
		}, []string{"outcome"}),
		rollupLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_status_rollup_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last status evaluation whose five capability buckets were persisted.",
		}),
		mutations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_status_incident_mutations_total",
			Help: "Public status event mutations by bounded kind, action, and outcome.",
		}, []string{"kind", "action", "outcome"}),
	}
	for _, outcome := range []string{"fresh", "stale", "error"} {
		m.evaluations.WithLabelValues(outcome)
	}
	for _, kind := range []string{"incident", "maintenance", "unknown"} {
		for _, action := range []string{"create", "update"} {
			for _, outcome := range []string{"ok", "rejected", "error"} {
				m.mutations.WithLabelValues(kind, action, outcome)
			}
		}
	}
	if err := registry.Register(m.evaluations); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.evaluations = duplicate.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	if err := registry.Register(m.rollupLastSuccess); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.rollupLastSuccess = duplicate.ExistingCollector.(prometheus.Gauge)
		} else {
			panic(err)
		}
	}
	if err := registry.Register(m.mutations); err != nil {
		var duplicate prometheus.AlreadyRegisteredError
		if errors.As(err, &duplicate) {
			m.mutations = duplicate.ExistingCollector.(*prometheus.CounterVec)
		} else {
			panic(err)
		}
	}
	return m
}

func (m *statusMetrics) observeEvaluation(outcome string) {
	if m == nil {
		return
	}
	switch outcome {
	case "fresh", "stale", "error":
	default:
		outcome = "error"
	}
	m.evaluations.WithLabelValues(outcome).Inc()
}

func (m *statusMetrics) markRollupSuccess(at time.Time) {
	if m != nil {
		m.rollupLastSuccess.Set(float64(at.UTC().Unix()))
	}
}

func (m *statusMetrics) observeMutation(kind, action, outcome string) {
	if m == nil {
		return
	}
	if kind != "incident" && kind != "maintenance" {
		kind = "unknown"
	}
	if action != "create" && action != "update" {
		action = "update"
	}
	if outcome != "ok" && outcome != "rejected" && outcome != "error" {
		outcome = "error"
	}
	m.mutations.WithLabelValues(kind, action, outcome).Inc()
}
