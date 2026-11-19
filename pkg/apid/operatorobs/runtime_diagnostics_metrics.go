package operatorobs

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	runtimePhaseInventory     = "inventory"
	runtimePhaseCoreEvidence  = "core_evidence"
	runtimePhaseAdvisoryReads = "loop_and_advisory"
	runtimePhaseAssembly      = "assembly"
	runtimePhasePreview       = "preview_fitting"
	runtimePhaseBefore        = "before_collection"
	runtimePhaseOutside       = "outside_phases"

	runtimeDeadlineBudget = "collection_budget"
	runtimeDeadlineCaller = "caller_deadline"
)

// The context still reports DeadlineExceeded. The cause identifies the timer
// that belongs to this collection rather than an inherited deadline.
var errRuntimeCollectionBudget = errors.New("runtime collection budget exhausted")

// RuntimeDiagnosticsMetrics observes this process's protected runtime reads.
// Labels contain only fixed diagnostic groups, evidence reasons, phases, deadline origins and outcomes.
// Collection completeness is independent of daemon health or release matching.
type RuntimeDiagnosticsMetrics struct {
	registry       prometheus.Registerer
	collectors     []prometheus.Collector
	admission      atomic.Pointer[RuntimeBuildAdmission]
	reports        *prometheus.CounterVec
	incomplete     *prometheus.CounterVec
	evaluations    *prometheus.CounterVec
	coreGaps       *prometheus.CounterVec
	lastComplete   prometheus.Gauge
	lastCollection runtimeLastCollection
	deadlines      *prometheus.CounterVec
	duration       prometheus.Histogram
	phaseDuration  *prometheus.HistogramVec
	inventory      *prometheus.CounterVec
	queries        *prometheus.CounterVec
	queryFailures  *prometheus.CounterVec
	queryDuration  *prometheus.HistogramVec
	responseBytes  prometheus.Histogram
	omitted        *prometheus.CounterVec
	active         prometheus.GaugeFunc
	refused        prometheus.Counter
}

func NewRuntimeDiagnosticsMetrics(reg prometheus.Registerer, admission *RuntimeBuildAdmission) *RuntimeDiagnosticsMetrics {
	m := &RuntimeDiagnosticsMetrics{
		registry:     reg,
		refused:      prometheus.NewCounter(prometheus.CounterOpts{Name: "apid_operator_runtime_refused_collections_total", Help: "Runtime collections refused at capacity before inventory or Prometheus reads. Excludes authentication, validation and preflight cancellation."}),
		reports:      prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_reports_total", Help: "Completed process-local runtime report collections by collection outcome, independent of daemon health."}, []string{"outcome"}),
		incomplete:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_incomplete_reports_total", Help: "Reports with the incomplete outcome, counted once for each evidence group with incomplete coverage."}, []string{"group"}),
		evaluations:  prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_group_evaluations_total", Help: "Evidence groups evaluated in complete or incomplete runtime reports, counted once per group by that group's completeness."}, []string{"group", "outcome"}),
		coreGaps:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_core_evidence_gaps_total", Help: "Incomplete core evidence, counted once per group and reason in each incomplete runtime report."}, []string{"group", "reason"}),
		lastComplete: prometheus.NewGauge(prometheus.GaugeOpts{Name: "apid_operator_runtime_last_complete_report_timestamp_seconds", Help: "Unix time of the last complete runtime report in this apid. Zero until one completes."}),
		lastCollection: runtimeLastCollection{
			desc: prometheus.NewDesc(
				"apid_operator_runtime_last_collection_timestamp_seconds",
				"Unix time of the latest finished runtime collection in this apid, labeled by its outcome. Zero until one finishes.",
				[]string{"outcome"}, nil,
			),
			outcome: "not_collected",
		},
		deadlines:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_collection_deadlines_total", Help: "Completed runtime reports with deadline_exceeded, counted once by the first phase to observe expiry and the governing deadline origin."}, []string{"phase", "origin"}),
		duration:      prometheus.NewHistogram(prometheus.HistogramOpts{Name: "apid_operator_runtime_collection_duration_seconds", Help: "Runtime report collection and preview fitting duration, including partial and failed source reads.", Buckets: prometheus.DefBuckets}),
		phaseDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apid_operator_runtime_phase_duration_seconds", Help: "Runtime collection wall-clock duration by phase. Parallel work is measured once per phase.", Buckets: prometheus.DefBuckets}, []string{"phase"}),
		inventory:     prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_inventory_attempts_total", Help: "Compute-backed runtime inventory assessments by outcome, including early failure before the registry read."}, []string{"outcome"}),
		queries:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_query_attempts_total", Help: "Runtime source-read invocations by fixed group and outcome, including preflight deadline or cancellation without an HTTP request."}, []string{"group", "outcome"}),
		queryFailures: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_query_source_errors_total", Help: "Runtime source reads with a source_error outcome, by fixed diagnostic group and failure reason."}, []string{"group", "reason"}),
		queryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "apid_operator_runtime_query_duration_seconds", Help: "Runtime source-read invocation duration, including response decoding and failures.", Buckets: prometheus.DefBuckets}, []string{"group"}),
		responseBytes: prometheus.NewHistogram(prometheus.HistogramOpts{Name: "apid_operator_runtime_response_bytes", Help: "Encoded runtime report payload size including the JSON newline, before writing to the client; not delivery acknowledgement.", Buckets: []float64{1024, 4096, 8192, 16384, 32768, 49152, 65536}}),
		omitted:       prometheus.NewCounterVec(prometheus.CounterOpts{Name: "apid_operator_runtime_preview_omitted_items_total", Help: "Preview items omitted in completed reports, including inventory preview and response envelope limits. Repeated reads count again; summaries retain full coverage."}, []string{"group"}),
	}
	m.active = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "apid_operator_runtime_active_collections",
		Help: "Current admitted runtime source collections in this apid, including unwinding canceled work.",
	}, func() float64 { return float64(m.admission.Load().Active()) })
	m.collectors = []prometheus.Collector{
		m.reports, m.incomplete, m.evaluations, m.coreGaps, m.lastComplete, &m.lastCollection,
		m.deadlines, m.duration, m.phaseDuration, m.inventory, m.queries, m.queryFailures,
		m.queryDuration, m.responseBytes, m.omitted, m.active, m.refused,
	}
	for _, phase := range []string{runtimePhaseInventory, runtimePhaseCoreEvidence, runtimePhaseAdvisoryReads, runtimePhaseAssembly, runtimePhasePreview, runtimePhaseBefore, runtimePhaseOutside} {
		if phase != runtimePhaseBefore && phase != runtimePhaseOutside {
			m.phaseDuration.WithLabelValues(phase)
		}
		m.deadlines.WithLabelValues(phase, runtimeDeadlineBudget)
		m.deadlines.WithLabelValues(phase, runtimeDeadlineCaller)
	}
	for _, outcome := range []string{"success", "invalid_inventory", "source_error", "deadline_exceeded", "canceled", "not_configured"} {
		m.inventory.WithLabelValues(outcome)
	}
	for _, outcome := range []string{"complete", "incomplete", "deadline_exceeded", "canceled", "unselected", "encoding_error"} {
		m.reports.WithLabelValues(outcome)
	}
	for _, group := range runtimeCoreMetricGroups() {
		for _, gap := range runtimeCoreEvidenceGaps(group, RuntimeBuildsLoopResponse{}) {
			m.coreGaps.WithLabelValues(group, gap.reason)
		}
	}
	for _, group := range runtimeMetricGroups() {
		m.incomplete.WithLabelValues(group)
		m.evaluations.WithLabelValues(group, "complete")
		m.evaluations.WithLabelValues(group, "incomplete")
		for _, outcome := range []string{"success", "source_error", "deadline_exceeded", "canceled", "not_configured"} {
			m.queries.WithLabelValues(group, outcome)
		}
		for _, reason := range []string{promql.VectorFailureTransport, promql.VectorFailureHTTP, promql.VectorFailureInvalidResponse, promql.VectorFailureOther} {
			m.queryFailures.WithLabelValues(group, reason)
		}
		m.queryDuration.WithLabelValues(group)
		if group != "readiness" && group != "process" {
			m.omitted.WithLabelValues(group)
		}
	}
	m.BindAdmission(admission)
	err := reg.Register(m)
	if err == nil {
		return m
	}
	var alreadyRegistered prometheus.AlreadyRegisteredError
	if errors.As(err, &alreadyRegistered) {
		if existing, ok := alreadyRegistered.ExistingCollector.(*RuntimeDiagnosticsMetrics); ok {
			existing.BindAdmission(admission)
			return existing
		}
	}
	panic(err)
}

// BindAdmission keeps the gauge attached to the server using this registry.
// The registry may outlive the server, so reusing it preserves observations.
func (m *RuntimeDiagnosticsMetrics) BindAdmission(admission *RuntimeBuildAdmission) {
	m.admission.Store(admission)
}

func (m *RuntimeDiagnosticsMetrics) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range m.collectors {
		collector.Describe(ch)
	}
}

func (m *RuntimeDiagnosticsMetrics) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range m.collectors {
		collector.Collect(ch)
	}
}

// Emit one timestamp and outcome from the same snapshot, including when a
// scrape overlaps concurrent collections finishing with different outcomes.
type runtimeLastCollection struct {
	desc    *prometheus.Desc
	mu      sync.Mutex
	at      float64
	outcome string
}

func (m *runtimeLastCollection) Describe(ch chan<- *prometheus.Desc) {
	ch <- m.desc
}

func (m *runtimeLastCollection) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	at, outcome := m.at, m.outcome
	m.mu.Unlock()
	ch <- prometheus.MustNewConstMetric(m.desc, prometheus.GaugeValue, at, outcome)
}

func (m *RuntimeDiagnosticsMetrics) Registry() prometheus.Registerer {
	if m == nil {
		return nil
	}
	return m.registry
}

// RuntimeBuilds preserves the existing collector and its source budget. A nil
// receiver records nothing and supports callers without a metrics registry.
func (m *RuntimeDiagnosticsMetrics) RuntimeBuilds(ctx context.Context, client *promql.Client, registry RuntimeBuildRegistry, selected RuntimeBuildSelection, now time.Time) RuntimeBuildsLoopResponse {
	if m != nil {
		var deadlinePhase string
		if ctx.Err() == context.DeadlineExceeded {
			deadlinePhase = runtimePhaseBefore
		}
		ctx = context.WithValue(ctx, runtimeQueryObservationKey{}, runtimeQueryObservation{metrics: m, group: "identity", deadlinePhase: &deadlinePhase})
	}
	return RuntimeBuildsWithLoopHealth(ctx, client, registry, selected, now)
}

type runtimeQueryObservationKey struct{}
type runtimeQueryObservation struct {
	metrics *RuntimeDiagnosticsMetrics
	group   string
	// Only the collection coordinator reads or updates deadlinePhase. Query
	// workers get copies of the observation and only record query metrics.
	deadlinePhase *string
}

func runtimeObservation(ctx context.Context) runtimeQueryObservation {
	observation, _ := ctx.Value(runtimeQueryObservationKey{}).(runtimeQueryObservation)
	return observation
}

func (o runtimeQueryObservation) observePhase(ctx context.Context, phase string, started time.Time) {
	if o.metrics == nil {
		return
	}
	if o.deadlinePhase != nil && *o.deadlinePhase == "" && ctx.Err() == context.DeadlineExceeded {
		*o.deadlinePhase = runtimePhaseOutside
		if deadline, ok := ctx.Deadline(); ok && !deadline.Before(started) {
			*o.deadlinePhase = phase
		}
	}
	o.metrics.phaseDuration.WithLabelValues(phase).Observe(time.Since(started).Seconds())
}

func (o runtimeQueryObservation) recordInventory(ctx context.Context, started time.Time, outcome string) {
	if o.metrics != nil {
		o.observePhase(ctx, runtimePhaseInventory, started)
		o.metrics.inventory.WithLabelValues(outcome).Inc()
	}
}

func runtimeCoreMetricGroups() []string {
	return []string{"identity", "readiness", "process", "loop_health"}
}

func runtimeMetricGroups() []string {
	return append(runtimeCoreMetricGroups(), RuntimeDiagnosticGroups("single-box-v2")...)
}

func runtimeMetricGroupKnown(group string) bool {
	return slices.Contains(runtimeCoreMetricGroups(), group) || slices.Contains(RuntimeDiagnosticGroups("single-box-v2"), group)
}

func runtimeQueryContext(ctx context.Context, group string) context.Context {
	if observation, ok := ctx.Value(runtimeQueryObservationKey{}).(runtimeQueryObservation); ok {
		observation.group = group
		return context.WithValue(ctx, runtimeQueryObservationKey{}, observation)
	}
	return ctx
}

func (o runtimeQueryObservation) record(started time.Time, err error) {
	if o.metrics == nil || !runtimeMetricGroupKnown(o.group) {
		return
	}
	outcome := runtimeReadOutcome(err)
	o.metrics.queries.WithLabelValues(o.group, outcome).Inc()
	if outcome == "source_error" {
		o.metrics.queryFailures.WithLabelValues(o.group, promql.VectorQueryFailure(err)).Inc()
	}
	o.metrics.queryDuration.WithLabelValues(o.group).Observe(time.Since(started).Seconds())
}

func runtimeReadOutcome(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, errNotificationNotConfigured):
		return "not_configured"
	case err != nil:
		return "source_error"
	default:
		return "success"
	}
}

func (m *RuntimeDiagnosticsMetrics) recordReport(ctx context.Context, out RuntimeBuildsLoopResponse, started time.Time) {
	m.duration.Observe(time.Since(started).Seconds())
	outcome := "complete"
	groups, incomplete := m.recordReportGroups(out)
	if incomplete {
		outcome = "incomplete"
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		outcome = "deadline_exceeded"
	case errors.Is(ctx.Err(), context.Canceled):
		outcome = "canceled"
	case len(out.Jobs) == 0:
		outcome = "unselected"
	}
	if body, err := json.Marshal(out); err == nil {
		m.responseBytes.Observe(float64(len(body) + 1))
	} else {
		outcome = "encoding_error"
	}
	m.reports.WithLabelValues(outcome).Inc()
	if outcome == "complete" || outcome == "incomplete" {
		for _, group := range groups {
			groupOutcome := "complete"
			if group.incomplete {
				groupOutcome = "incomplete"
				m.incomplete.WithLabelValues(group.name).Inc()
				m.recordCoreEvidenceGaps(group.name, out)
			}
			m.evaluations.WithLabelValues(group.name, groupOutcome).Inc()
		}
	}
	if outcome == "deadline_exceeded" {
		phase := runtimePhaseOutside
		if observed := runtimeObservation(ctx).deadlinePhase; observed != nil && *observed != "" {
			phase = *observed
		}
		origin := runtimeDeadlineCaller
		if errors.Is(context.Cause(ctx), errRuntimeCollectionBudget) {
			origin = runtimeDeadlineBudget
		}
		m.deadlines.WithLabelValues(phase, origin).Inc()
	}
	m.lastCollection.mu.Lock()
	at := float64(time.Now().UnixNano()) / float64(time.Second)
	m.lastCollection.at, m.lastCollection.outcome = at, outcome
	if outcome == "complete" {
		m.lastComplete.Set(at)
	}
	m.lastCollection.mu.Unlock()
}
