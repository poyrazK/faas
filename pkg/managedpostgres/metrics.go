package managedpostgres

import (
	"errors"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics is the low-cardinality observability surface for the managed
// PostgreSQL control-plane loops. It intentionally never labels a series by
// account, app, database, provider resource, or error text: those values are
// either customer data or unbounded input and belong in the structured audit
// trail instead.
type Metrics struct {
	reconcileTotal       *prometheus.CounterVec
	reconcileDuration    *prometheus.HistogramVec
	usageDatabaseTotal   *prometheus.CounterVec
	usageSweepTotal      *prometheus.CounterVec
	usageDatabases       *prometheus.GaugeVec
	usageLastSuccess     prometheus.Gauge
	usagePolicyEnabled   prometheus.Gauge
	provisioningEnabled  prometheus.Gauge
	canaryAdmissionTotal *prometheus.CounterVec
	admissionDeniedTotal *prometheus.CounterVec
}

const (
	metricsResourceDatabase = "database"
	metricsResourceBinding  = "binding"
	metricsOperationCreate  = "provisioning"
	metricsOperationDelete  = "deleting"

	metricsOutcomeCompleted = "completed"
	metricsOutcomeDeferred  = "deferred"
	metricsOutcomeContended = "contended"
	metricsOutcomeFailed    = "failed"

	metricsUsageRecorded = "recorded"
	metricsUsageDeferred = "deferred"

	metricsSweepSuccess  = "success"
	metricsSweepDegraded = "degraded"
	metricsSweepError    = "error"
	metricsSweepDisabled = "disabled"

	metricsAdmissionAllowed = "allowed"
	metricsAdmissionDenied  = "denied"

	metricsDenialInvalid     = "invalid"
	metricsDenialUnavailable = "unavailable"
	metricsDenialQuota       = "quota"
	metricsDenialStale       = "usage_stale"
	metricsDenialUnsupported = "unsupported"
	metricsDenialConflict    = "conflict"
)

// NewMetrics registers the managed-PostgreSQL metric families on reg. The
// prefix should be the daemon prefix (normally "apid") so the metrics remain
// namespaced with the rest of that daemon's /metrics endpoint. A nil
// registerer disables the optional surface and returns a nil bundle.
func NewMetrics(reg prometheus.Registerer, prefix string, usageEnabled bool) (*Metrics, error) {
	if reg == nil {
		return nil, nil
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "_")
	if prefix == "" {
		return nil, ErrInvalid
	}
	m := &Metrics{
		reconcileTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_reconcile_total",
			Help: "Managed PostgreSQL lifecycle reconciliation attempts by resource, operation, and outcome.",
		}, []string{"resource", "operation", "outcome"}),
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: prefix + "_managed_postgres_reconcile_duration_seconds",
			Help: "Managed PostgreSQL lifecycle reconciliation duration in seconds.",
		}, []string{"resource", "operation"}),
		usageDatabaseTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_usage_collection_databases_total",
			Help: "Managed PostgreSQL database usage collection outcomes.",
		}, []string{"outcome"}),
		usageSweepTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_usage_collection_sweeps_total",
			Help: "Managed PostgreSQL usage collection sweeps by outcome.",
		}, []string{"outcome"}),
		usageDatabases: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_usage_collection_databases",
			Help: "Managed PostgreSQL databases discovered, recorded, or deferred by the latest usage sweep.",
		}, []string{"state"}),
		usageLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_usage_last_success_timestamp_seconds",
			Help: "Unix timestamp of the latest complete managed PostgreSQL usage sweep; zero means none has succeeded.",
		}),
		usagePolicyEnabled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_usage_policy_enabled",
			Help: "Whether the managed PostgreSQL provider usage ledger is enabled.",
		}),
		provisioningEnabled: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: prefix + "_managed_postgres_provisioning_enabled",
			Help: "Whether the managed PostgreSQL customer provisioning gate is currently open.",
		}),
		canaryAdmissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_canary_admission_total",
			Help: "Managed PostgreSQL staging canary gate decisions.",
		}, []string{"outcome"}),
		admissionDeniedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: prefix + "_managed_postgres_admission_denied_total",
			Help: "Managed PostgreSQL admission denials by stable reason.",
		}, []string{"reason"}),
	}
	collectors := []prometheus.Collector{
		m.reconcileTotal, m.reconcileDuration, m.usageDatabaseTotal,
		m.usageSweepTotal, m.usageDatabases, m.usageLastSuccess,
		m.usagePolicyEnabled, m.provisioningEnabled, m.canaryAdmissionTotal,
		m.admissionDeniedTotal,
	}
	for _, collector := range collectors {
		if err := reg.Register(collector); err != nil {
			return nil, err
		}
	}
	for _, resource := range []string{metricsResourceDatabase, metricsResourceBinding} {
		for _, operation := range []string{metricsOperationCreate, metricsOperationDelete} {
			for _, outcome := range []string{metricsOutcomeCompleted, metricsOutcomeDeferred, metricsOutcomeContended, metricsOutcomeFailed} {
				m.reconcileTotal.WithLabelValues(resource, operation, outcome).Add(0)
			}
			m.reconcileDuration.WithLabelValues(resource, operation)
		}
	}
	for _, outcome := range []string{metricsUsageRecorded, metricsUsageDeferred} {
		m.usageDatabaseTotal.WithLabelValues(outcome).Add(0)
	}
	for _, outcome := range []string{metricsSweepSuccess, metricsSweepDegraded, metricsSweepError, metricsSweepDisabled} {
		m.usageSweepTotal.WithLabelValues(outcome).Add(0)
	}
	for _, state := range []string{"discovered", "recorded", "deferred"} {
		m.usageDatabases.WithLabelValues(state).Set(0)
	}
	for _, outcome := range []string{metricsAdmissionAllowed, metricsAdmissionDenied} {
		m.canaryAdmissionTotal.WithLabelValues(outcome).Add(0)
	}
	for _, reason := range []string{metricsDenialInvalid, metricsDenialUnavailable, metricsDenialQuota, metricsDenialStale, metricsDenialUnsupported, metricsDenialConflict} {
		m.admissionDeniedTotal.WithLabelValues(reason).Add(0)
	}
	m.usagePolicyEnabled.Set(boolGauge(usageEnabled))
	m.usageLastSuccess.Set(0)
	m.provisioningEnabled.Set(0)
	return m, nil
}

func boolGauge(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

// SetProvisioningEnabled records the latest evaluated rollout gate.
func (m *Metrics) SetProvisioningEnabled(enabled bool) {
	if m != nil {
		m.provisioningEnabled.Set(boolGauge(enabled))
	}
}

// ObserveCanary records a closed-vocabulary canary decision.
func (m *Metrics) ObserveCanary(allowed bool) {
	if m == nil {
		return
	}
	outcome := metricsAdmissionDenied
	if allowed {
		outcome = metricsAdmissionAllowed
	}
	m.canaryAdmissionTotal.WithLabelValues(outcome).Inc()
}

// ObserveAdmission records only admission failures. Successful admissions do
// not need a counter because the reservation and reconcile metrics provide
// the corresponding success evidence.
func (m *Metrics) ObserveAdmission(err error) {
	if m == nil || err == nil {
		return
	}
	reason := metricsDenialUnavailable
	switch {
	case errors.Is(err, ErrInvalid):
		reason = metricsDenialInvalid
	case errors.Is(err, ErrQuotaExceeded):
		reason = metricsDenialQuota
	case errors.Is(err, ErrUsageStale):
		reason = metricsDenialStale
	case errors.Is(err, ErrUnsupported):
		reason = metricsDenialUnsupported
	case errors.Is(err, ErrConflict):
		reason = metricsDenialConflict
	}
	m.admissionDeniedTotal.WithLabelValues(reason).Inc()
}

// ObserveReconcile records a database lifecycle sweep observation.
func (m *Metrics) ObserveReconcile(observation ReconcileObservation) {
	if m == nil {
		return
	}
	operation := metricsOperationCreate
	if observation.Operation == StateDeleting {
		operation = metricsOperationDelete
	}
	outcome := string(observation.Outcome)
	switch outcome {
	case metricsOutcomeCompleted, metricsOutcomeDeferred, metricsOutcomeContended, metricsOutcomeFailed:
	default:
		return
	}
	m.reconcileTotal.WithLabelValues(metricsResourceDatabase, operation, outcome).Inc()
	if observation.Duration > 0 {
		m.reconcileDuration.WithLabelValues(metricsResourceDatabase, operation).Observe(observation.Duration.Seconds())
	}
}

// ObserveBindingReconcile records a binding lifecycle sweep observation.
func (m *Metrics) ObserveBindingReconcile(observation BindingReconcileObservation) {
	if m == nil {
		return
	}
	operation := metricsOperationCreate
	if observation.Operation == BindingStateDeleting {
		operation = metricsOperationDelete
	}
	outcome := string(observation.Outcome)
	switch outcome {
	case metricsOutcomeCompleted, metricsOutcomeDeferred, metricsOutcomeContended, metricsOutcomeFailed:
	default:
		return
	}
	m.reconcileTotal.WithLabelValues(metricsResourceBinding, operation, outcome).Inc()
	if observation.Duration > 0 {
		m.reconcileDuration.WithLabelValues(metricsResourceBinding, operation).Observe(observation.Duration.Seconds())
	}
}

// ObserveUsage records a per-database usage outcome.
func (m *Metrics) ObserveUsage(observation UsageCollectionObservation) {
	if m == nil {
		return
	}
	switch observation.Outcome {
	case metricsUsageRecorded, metricsUsageDeferred:
		m.usageDatabaseTotal.WithLabelValues(observation.Outcome).Inc()
	}
}

// ObserveUsageSweep records the latest usage sweep and its freshness point.
func (m *Metrics) ObserveUsageSweep(summary UsageCollectionSummary, sweepErr error) {
	if m == nil {
		return
	}
	m.usageDatabases.WithLabelValues("discovered").Set(float64(summary.Discovered))
	m.usageDatabases.WithLabelValues("recorded").Set(float64(summary.Recorded))
	m.usageDatabases.WithLabelValues("deferred").Set(float64(summary.Deferred))
	if !summary.Enabled {
		m.usageSweepTotal.WithLabelValues(metricsSweepDisabled).Inc()
		return
	}
	if sweepErr != nil {
		m.usageSweepTotal.WithLabelValues(metricsSweepError).Inc()
		return
	}
	if summary.Deferred > 0 {
		m.usageSweepTotal.WithLabelValues(metricsSweepDegraded).Inc()
		return
	}
	m.usageSweepTotal.WithLabelValues(metricsSweepSuccess).Inc()
	if !summary.CompletedAt.IsZero() {
		m.usageLastSuccess.Set(float64(summary.CompletedAt.UTC().Unix()))
	}
}
