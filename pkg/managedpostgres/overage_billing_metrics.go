package managedpostgres

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// BillingMetrics exports the overage billing sweep (ADR-20261031165104).
type BillingMetrics struct {
	subjects  *prometheus.GaugeVec
	steps     *prometheus.CounterVec
	charged   *prometheus.CounterVec
	lastSweep prometheus.Gauge
}

// NewBillingMetrics registers the families on reg under the daemon prefix. A
// nil registerer returns a nil bundle, whose Observe does nothing.
func NewBillingMetrics(reg prometheus.Registerer, prefix string) (*BillingMetrics, error) {
	if reg == nil {
		return nil, nil
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		return nil, ErrInvalid
	}
	m := &BillingMetrics{
		subjects: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_billing_subjects",
			Help: "Managed PostgreSQL usage subjects in the latest overage billing sweep, by what the sweep did with them.",
		}, []string{"state"}),
		steps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_billing_steps_total",
			Help: "Managed PostgreSQL overage billing steps by mode; shadow steps are recorded, not sent.",
		}, []string{"mode"}),
		charged: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_billing_charged_millicents_total",
			Help: "Managed PostgreSQL overage millicents delivered by billing steps, by mode; shadow is what live would have billed.",
		}, []string{"mode"}),
		lastSweep: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_billing_last_sweep_timestamp_seconds",
			Help: "Unix timestamp of the latest overage billing sweep with no failed subject; zero means none.",
		}),
	}
	for _, c := range []prometheus.Collector{m.subjects, m.steps, m.charged, m.lastSweep} {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Observe records one sweep. An off sweep (no mode) records nothing.
func (m *BillingMetrics) Observe(summary BillingSweepSummary) {
	if m == nil || summary.Mode == "" {
		return
	}
	for state, n := range map[string]int{"stepped": summary.Stepped, "closed": summary.Closed, "not_billable": summary.NotBillable,
		"stale": summary.Stale, "not_due": summary.NotDue, "failed": summary.Failed} {
		m.subjects.WithLabelValues(state).Set(float64(n))
	}
	m.steps.WithLabelValues(summary.Mode).Add(float64(summary.Stepped + summary.Closed))
	m.charged.WithLabelValues(summary.Mode).Add(float64(summary.StepMillicents))
	if summary.Failed == 0 && !summary.CompletedAt.IsZero() {
		m.lastSweep.Set(float64(summary.CompletedAt.UTC().Unix()))
	}
}
