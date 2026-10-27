package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Loop labels of meterd's customer observability loops.
const (
	loopSyntheticChecks = "synthetic_checks" // ADR-748
	loopSLORollup       = "slo_rollup"       // ADR-747
)

// observabilityLoops are the health series of meterd's customer
// observability loops (ADR-20261024222102), so a stalled or disabled loop
// pages an operator instead of leaving customers' synthetic results and SLO
// budgets silently stale. A nil value records nothing.
type observabilityLoops struct {
	enabled  *prometheus.GaugeVec
	lastPass *prometheus.GaugeVec
	passes   *prometheus.CounterVec
	items    *prometheus.CounterVec
}

// loopItemOutcomes are each loop's per-item outcomes, created at zero so
// increase() sees the first event.
var loopItemOutcomes = map[string][]string{
	loopSyntheticChecks: {"ok", "failed", "record_error"},
	loopSLORollup:       {"recorded", "failed"},
}

func newObservabilityLoops(reg prometheus.Registerer) *observabilityLoops {
	l := &observabilityLoops{
		enabled: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_observability_loop_enabled",
			Help: "1 while a customer observability loop runs: synthetic_checks (ADR-748), or slo_rollup (ADR-747), which also needs FAAS_PROMETHEUS_URL.",
		}, []string{"loop"}),
		lastPass: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "meterd_observability_loop_last_pass_timestamp_seconds",
			Help: "Unix time of the loop's last pass that finished without an error; 0 until the first.",
		}, []string{"loop"}),
		passes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_observability_loop_passes_total",
			Help: "Customer observability loop passes by outcome (ok, error).",
		}, []string{"loop", "outcome"}),
		items: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "meterd_observability_loop_items_total",
			Help: "Work items of a customer observability loop by outcome: synthetic_checks runs (ok, failed: the customer's check failed, record_error) and slo_rollup hours (recorded, failed).",
		}, []string{"loop", "outcome"}),
	}
	for loop, outcomes := range loopItemOutcomes {
		l.enabled.WithLabelValues(loop).Set(0)
		l.lastPass.WithLabelValues(loop).Set(0)
		l.passes.WithLabelValues(loop, "ok").Add(0)
		l.passes.WithLabelValues(loop, "error").Add(0)
		for _, outcome := range outcomes {
			l.items.WithLabelValues(loop, outcome).Add(0)
		}
	}
	if reg != nil {
		reg.MustRegister(l.enabled, l.lastPass, l.passes, l.items)
	}
	return l
}

func (l *observabilityLoops) setEnabled(loop string, on bool) {
	if l == nil {
		return
	}
	value := 0.0
	if on {
		value = 1
	}
	l.enabled.WithLabelValues(loop).Set(value)
}

// pass records one finished pass; only an error-free pass moves lastPass.
func (l *observabilityLoops) pass(loop string, err error, at time.Time) {
	if l == nil {
		return
	}
	if err != nil {
		l.passes.WithLabelValues(loop, "error").Inc()
		return
	}
	l.passes.WithLabelValues(loop, "ok").Inc()
	l.lastPass.WithLabelValues(loop).Set(float64(at.Unix()))
}

func (l *observabilityLoops) add(loop, outcome string, n int) {
	if l == nil || n <= 0 {
		return
	}
	l.items.WithLabelValues(loop, outcome).Add(float64(n))
}
