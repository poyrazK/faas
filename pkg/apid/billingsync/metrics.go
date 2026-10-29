package billingsync

import (
	"errors"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/billing"
)

// Pass outcomes.
const (
	passCompleted = "completed"
	passSkipped   = "skipped"
	passFailed    = "failed"
)

// Metrics counts reconciliation passes and corrections. Its methods are
// nil-receiver safe.
type Metrics struct {
	registry    *prometheus.Registry
	passes      *prometheus.CounterVec
	corrections *prometheus.CounterVec
}

// NewMetrics registers the reconciliation metrics on registry.
func NewMetrics(registry *prometheus.Registry, prefix string) *Metrics {
	m := &Metrics{registry: registry}
	m.passes = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_billing_reconcile_passes_total",
		Help: "Billing subscription reconciliation passes by result: completed, skipped (another replica holds the pass) or failed.",
	}, []string{"result"}))
	m.corrections = register(registry, prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: prefix + "_billing_reconcile_corrections_total",
		Help: "Accounts reconciliation moved to their provider subscription's state, by the billing event applied. Any increase means a webhook delivery was missed.",
	}, []string{"correction"}))
	for _, result := range []string{passCompleted, passSkipped, passFailed} {
		m.passes.WithLabelValues(result)
	}
	for _, t := range []billing.EventType{billing.EventPaymentSucceeded, billing.EventPaymentFailed,
		billing.EventSubscriptionUpdated, billing.EventSubscriptionCanceled} {
		m.corrections.WithLabelValues(t.Name())
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

func (m *Metrics) observePass(result string) {
	if m != nil {
		m.passes.WithLabelValues(result).Inc()
	}
}

func (m *Metrics) observeCorrection(correction string) {
	if m != nil {
		m.corrections.WithLabelValues(correction).Inc()
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
