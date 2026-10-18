package builderd

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	requeueNoSlot       = "no_slot"
	requeueSource       = "source_unavailable"
	requeueCancellation = "context_canceled"
	requeueLookup       = "lookup_failure"
)

// PipelineMetrics observes admission without owning it. Slot gauges read the
// allocator under its existing lock; counters contain only fixed vocabularies.
type PipelineMetrics struct {
	builder                     *Builderd
	used, limit                 *prometheus.Desc
	claims, admission, requeues *prometheus.CounterVec
}

// WithPipelineMetrics attaches queue diagnostics before processing starts.
// It does not change the single-slot fence, claim selection, or requeue policy.
func (b *Builderd) WithPipelineMetrics(reg prometheus.Registerer) *Builderd {
	m := &PipelineMetrics{
		builder:   b,
		used:      prometheus.NewDesc("builderd_build_slots_in_use", "Process-local reserved VM build slots, read from the allocator. Excludes cache hits and preparation outside the slot.", nil, nil),
		limit:     prometheus.NewDesc("builderd_build_slots_limit", "Enforced process-local VM build slot limit, not opportunistic eligibility.", nil, nil),
		claims:    prometheus.NewCounterVec(prometheus.CounterOpts{Name: "builderd_build_claims_total", Help: "Completed claim calls by source (poll or notification) and outcome (claimed, not_claimed, error, canceled). Not_claimed does not establish an empty fleet queue."}, []string{"source", "outcome"}),
		admission: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "builderd_build_slot_decisions_total", Help: "Slot allocation decisions by outcome (granted, limit, policy). Limit denial is normal saturation, not a build failure."}, []string{"outcome"}),
		requeues:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "builderd_build_requeues_total", Help: "Completed requeue calls by fixed reason and outcome (accepted, not_requeued, error, canceled). Not_requeued includes fenced stale claims; accepted means the store call succeeded."}, []string{"reason", "outcome"}),
	}
	for _, source := range []string{"poll", "notification"} {
		for _, outcome := range []string{"claimed", "not_claimed", "error", "canceled"} {
			m.claims.WithLabelValues(source, outcome)
		}
	}
	for _, outcome := range []string{"granted", "limit", "policy"} {
		m.admission.WithLabelValues(outcome)
	}
	for _, reason := range []string{requeueNoSlot, requeueSource, requeueCancellation, requeueLookup} {
		for _, outcome := range []string{"accepted", "not_requeued", "error", "canceled"} {
			m.requeues.WithLabelValues(reason, outcome)
		}
	}
	reg.MustRegister(m)
	b.pipeline = m
	return b
}

func (m *PipelineMetrics) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.used
	ch <- m.limit
	m.claims.Describe(ch)
	m.admission.Describe(ch)
	m.requeues.Describe(ch)
}

func (m *PipelineMetrics) Collect(ch chan<- prometheus.Metric) {
	m.builder.slotMu.Lock()
	used := m.builder.activeSlots
	m.builder.slotMu.Unlock()
	ch <- prometheus.MustNewConstMetric(m.used, prometheus.GaugeValue, float64(used))
	ch <- prometheus.MustNewConstMetric(m.limit, prometheus.GaugeValue, float64(maxBuilderSlots))
	m.claims.Collect(ch)
	m.admission.Collect(ch)
	m.requeues.Collect(ch)
}

func (m *PipelineMetrics) observeClaim(ctx context.Context, source string, err error) {
	if m != nil {
		m.claims.WithLabelValues(source, storeCallOutcome(ctx, err, "claimed", "not_claimed")).Inc()
	}
}

func (m *PipelineMetrics) observeAdmission(outcome string) {
	if m != nil {
		m.admission.WithLabelValues(outcome).Inc()
	}
}

func (m *PipelineMetrics) observeRequeue(ctx context.Context, reason string, err error) {
	if m != nil {
		m.requeues.WithLabelValues(reason, storeCallOutcome(ctx, err, "accepted", "not_requeued")).Inc()
	}
}

func storeCallOutcome(ctx context.Context, err error, success, notFound string) string {
	switch {
	case err == nil:
		return success
	case errors.Is(err, state.ErrNotFound):
		return notFound
	case ctx.Err() != nil && errors.Is(err, ctx.Err()):
		return "canceled"
	default:
		return "error"
	}
}
