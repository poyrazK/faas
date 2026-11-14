package edgerules

import (
	"errors"
	"time"

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

	headerBackfillPasses   *prometheus.CounterVec
	headerBackfillValues   *prometheus.CounterVec
	headerBackfillFleet    prometheus.Gauge
	headerBackfillPending  prometheus.Gauge
	headerBackfillLastPass prometheus.Gauge
}

// Header backfill pass results (ADR-20261108110000, amendment 2026-11-09).
const (
	headerBackfillComplete      = "complete"
	headerBackfillPendingResult = "pending"
	headerBackfillFleetNotReady = "fleet_not_ready"
	headerBackfillError         = "error"
)

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
	m.headerBackfillPasses = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_edge_rule_header_backfill_passes_total",
		Help: "Background passes sealing stored edge-rule header credentials, by result: complete (nothing left), pending (values left to a later pass), fleet_not_ready (a serving gateway cannot open sealed values yet) or error.",
	}, []string{"result"}))
	for _, result := range []string{headerBackfillComplete, headerBackfillPendingResult, headerBackfillFleetNotReady, headerBackfillError} {
		m.headerBackfillPasses.WithLabelValues(result)
	}
	m.headerBackfillValues = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_edge_rule_header_backfill_items_total",
		Help: "Rules, rule-set versions and environment policies the header backfill handled, by kind and outcome: sealed, changed (written by someone after the pass read it; retried next pass) or failed.",
	}, []string{"kind", "outcome"}))
	for _, kind := range []string{"rule", "version", "environment_policy"} {
		for _, outcome := range []string{"sealed", "changed", "failed"} {
			m.headerBackfillValues.WithLabelValues(kind, outcome)
		}
	}
	m.headerBackfillFleet = register(registry, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_edge_rule_header_fleet_ready",
		Help: "1 when every serving gateway reported, at the last backfill pass, that it opens sealed edge-rule header values; 0 while apid stores new credentials as written.",
	}))
	m.headerBackfillPending = register(registry, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_edge_rule_header_backfill_pending",
		Help: "1 when the last header backfill pass left credentials stored as written (fleet not ready, items changed or failed, or the pass failed).",
	}))
	m.headerBackfillLastPass = register(registry, prometheus.NewGauge(prometheus.GaugeOpts{
		Name: prefix + "_edge_rule_header_backfill_last_pass_timestamp_seconds",
		Help: "Unix time the last header backfill pass ended.",
	}))
	return m
}

// headerBackfillPass records one backfill pass.
func (m *Metrics) headerBackfillPass(rep HeaderSecretBackfillReport, err error, now time.Time) {
	if m == nil {
		return
	}
	result := headerBackfillComplete
	switch {
	case err != nil:
		result = headerBackfillError
	case rep.FleetNotReady:
		result = headerBackfillFleetNotReady
	case rep.Pending():
		result = headerBackfillPendingResult
	}
	m.headerBackfillPasses.WithLabelValues(result).Inc()
	for _, item := range []struct {
		kind                    string
		sealed, changed, failed int
	}{
		{"rule", rep.RulesSealed, rep.RulesChanged, rep.RulesFailed},
		{"version", rep.VersionsSealed, rep.VersionsChanged, rep.VersionsFailed},
		{"environment_policy", rep.PoliciesSealed, rep.PoliciesChanged, rep.PoliciesFailed},
	} {
		m.headerBackfillValues.WithLabelValues(item.kind, "sealed").Add(float64(item.sealed))
		m.headerBackfillValues.WithLabelValues(item.kind, "changed").Add(float64(item.changed))
		m.headerBackfillValues.WithLabelValues(item.kind, "failed").Add(float64(item.failed))
	}
	if err == nil {
		m.headerBackfillFleet.Set(boolGauge(!rep.FleetNotReady))
	}
	m.headerBackfillPending.Set(boolGauge(err != nil || rep.Pending()))
	m.headerBackfillLastPass.Set(float64(now.Unix()))
}

func boolGauge(v bool) float64 {
	if v {
		return 1
	}
	return 0
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
