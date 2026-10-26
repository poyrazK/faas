package managedpostgres

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// CapMetrics exports the compute cap enforcer's sweeps (ADR-20261025094915).
type CapMetrics struct {
	mode        *prometheus.GaugeVec
	accounts    *prometheus.GaugeVec
	scopesTotal *prometheus.CounterVec
	lastSweep   prometheus.Gauge
}

var (
	capMetricModes         = []CapEnforcementMode{CapEnforcementOff, CapEnforcementShadow, CapEnforcementOn}
	capMetricAccountStates = []string{"enforced", "suspended", "uncapped", "period_mismatch", "unsupported", "failed"}
	capMetricScopeOutcomes = []string{"applied", "shadowed", "unchanged"}
)

// NewCapMetrics registers the families on reg under the daemon prefix. A nil
// registerer returns a nil bundle, whose Observe does nothing.
func NewCapMetrics(reg prometheus.Registerer, prefix string, mode CapEnforcementMode) (*CapMetrics, error) {
	if reg == nil {
		return nil, nil
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		return nil, ErrInvalid
	}
	m := &CapMetrics{
		mode: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_cap_enforcement_mode",
			Help: "Managed PostgreSQL compute cap enforcement mode; 1 for the configured mode.",
		}, []string{"mode"}),
		accounts: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_cap_accounts",
			Help: "Accounts with a running managed PostgreSQL database in the latest compute cap sweep, by state; suspended accounts are also enforced.",
		}, []string{"state"}),
		scopesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_cap_quota_decisions_total",
			Help: "Managed PostgreSQL compute quota decisions by outcome; shadowed decisions were computed but not sent.",
		}, []string{"outcome"}),
		lastSweep: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_cap_last_sweep_timestamp_seconds",
			Help: "Unix timestamp of the latest compute cap sweep with no failed account; zero means none.",
		}),
	}
	for _, collector := range []prometheus.Collector{m.mode, m.accounts, m.scopesTotal, m.lastSweep} {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	for _, candidate := range capMetricModes {
		m.mode.WithLabelValues(string(candidate)).Set(boolGauge(candidate == mode))
	}
	for _, state := range capMetricAccountStates {
		m.accounts.WithLabelValues(state).Set(0)
	}
	for _, outcome := range capMetricScopeOutcomes {
		m.scopesTotal.WithLabelValues(outcome).Add(0)
	}
	m.lastSweep.Set(0)
	return m, nil
}

func (m *CapMetrics) Observe(summary CapSweepSummary) {
	if m == nil || summary.Mode == CapEnforcementOff {
		return
	}
	skipped := summary.Uncapped + summary.PeriodMismatch + summary.Unsupported + summary.Failed
	m.accounts.WithLabelValues("enforced").Set(float64(max(0, summary.Accounts-skipped)))
	m.accounts.WithLabelValues("suspended").Set(float64(summary.SuspendedAccounts))
	m.accounts.WithLabelValues("uncapped").Set(float64(summary.Uncapped))
	m.accounts.WithLabelValues("period_mismatch").Set(float64(summary.PeriodMismatch))
	m.accounts.WithLabelValues("unsupported").Set(float64(summary.Unsupported))
	m.accounts.WithLabelValues("failed").Set(float64(summary.Failed))
	m.scopesTotal.WithLabelValues("applied").Add(float64(summary.ScopesApplied))
	m.scopesTotal.WithLabelValues("shadowed").Add(float64(summary.ScopesShadowed))
	m.scopesTotal.WithLabelValues("unchanged").Add(float64(summary.ScopesUnchanged))
	if summary.Failed == 0 && !summary.CompletedAt.IsZero() {
		m.lastSweep.Set(float64(summary.CompletedAt.UTC().Unix()))
	}
}
