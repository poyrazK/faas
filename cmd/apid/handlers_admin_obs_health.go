// handlers_admin_obs_health.go — Obs-Meta + Trace-IDs Mega-PR / C7:
// GET /v1/admin/obs/health. Answers the operator's meta-question
// "is the obs stack itself healthy?" by composing the per-metric
// computations in obs_health_query.go into a single JSON snapshot.
//
// Auth model: admin-only (same two-layer gate as the rest of
// /v1/admin/obs/*). The endpoint is read-only — does NOT emit a
// pii.accessed or operator.action.view audit row (the snapshot is
// meta-data about the audit pipeline itself; recording the read
// would inflate the audit_log row count and skew the completeness
// ratio we're measuring).
//
// Closed-set JSON shape: every response carries the same field
// names. Zero counts are 0, zero-row ratios are 1.0 (vacuous
// truth), absent kinds are seeded from obsHealthKindVocabulary so
// the dashboard never has to special-case a missing key.
//
// The PromQL-derived fields (audit_log_write_total_5m,
// audit_log_write_failures_5m, audit_log_coverage_ratio_5m,
// alerts_firing) retain stable numeric fallbacks on nil-promql or query
// failure. prometheus_available makes that degraded state explicit so a
// dashboard cannot render the fallback as observed telemetry.
// SQL-derived fields (operator_intent_outcome_missing_total,
// trace_id_completeness_ratio) DO surface store errors as 503
// because the local DB is the source of truth — a failure there
// IS the apid being unhealthy.
package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsHealthHandler handles GET /v1/admin/obs/health. The
// computation is split into per-metric methods in
// obs_health_query.go so this handler stays ≤50 lines and each
// PromQL / SQL call is testable in isolation.
//
// The snapshot is computed in series (not parallel) because the
// SQL calls share the same pool connection — running them in
// parallel would force three pool acquisitions instead of one
// chained tx, with no observable latency benefit at this scale
// (admin-only endpoint, polled on a 30s cadence).
func (s *server) obsHealthHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	ctx := r.Context()
	missing := seedHealthKindCounts()
	storeCounts, err := s.operatorIntentOutcomeMissing(ctx)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count stuck-running operator intents"))
		return
	}
	for kind, n := range storeCounts {
		missing[kind] = n
	}
	ratios := seedHealthKindRatios()
	storeRatios, err := s.traceIDCompletenessRatio(ctx)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not compute trace_id completeness ratio"))
		return
	}
	for kind, r := range storeRatios {
		ratios[kind] = r
	}
	auditWrites, auditWritesOK := s.auditLogWrite5mStatus(ctx)
	auditFailures, auditFailuresOK := s.auditLogWriteFailures5mStatus(ctx)
	auditCoverage, auditCoverageOK := s.auditLogCoverageRatio5mStatus(ctx)
	alerts, alertsOK := s.alertsFiringStatus(ctx)
	writeJSON(w, http.StatusOK, api.ObsHealthResponse{
		GeneratedAt:                        time.Now().UTC(),
		PrometheusAvailable:                auditWritesOK && auditFailuresOK && auditCoverageOK && alertsOK,
		AuditLogWriteTotal5m:               auditWrites,
		AuditLogWriteFailures5m:            auditFailures,
		AuditLogCoverageRatio5m:            auditCoverage,
		OperatorIntentOutcomeMissingCounts: missing,
		TraceIDCompletenessRatio:           ratios,
		AlertsFiring:                       alerts,
	})
}
