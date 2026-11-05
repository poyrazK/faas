package edgerules

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// Convergence phases label failures (ADR-091 amendment 2026-10-27, later).
const (
	ConvergencePhasePrepare = "prepare"
	ConvergencePhaseApply   = "apply"
)

// Metrics counts what the edge-rule convergence barrier did. Its methods are
// nil-receiver safe, so a Service built without a registry records nothing.
type Metrics struct {
	registry        *prometheus.Registry
	skippedGateways *prometheus.CounterVec
	failures        *prometheus.CounterVec
}

// NewMetrics registers the convergence metrics on registry.
func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	m := &Metrics{registry: registry}
	m.skippedGateways = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_edge_rule_convergence_skipped_gateways_total",
		Help: "Edge-rule mutations that left a gateway out of the convergence barrier because its edge listener stopped stamping its heartbeat, by gateway node.",
	}, []string{"node"}))
	m.failures = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_edge_rule_convergence_failures_total",
		Help: "Edge-rule convergence phases that did not finish: prepare (the mutation was refused with 503 and nothing changed) or apply (the mutation committed but a gateway did not acknowledge it; its hosts stay held until the fence expires).",
	}, []string{"phase"}))
	for _, phase := range []string{ConvergencePhasePrepare, ConvergencePhaseApply} {
		m.failures.WithLabelValues(phase)
	}
	return m
}

// Registry is the registry the metrics were registered on.
func (m *Metrics) Registry() *prometheus.Registry {
	if m == nil {
		return nil
	}
	return m.registry
}

func (m *Metrics) gatewaySkipped(node string) {
	if m != nil {
		m.skippedGateways.WithLabelValues(node).Inc()
	}
}

func (m *Metrics) convergenceFailed(phase string) {
	if m != nil {
		m.failures.WithLabelValues(phase).Inc()
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
