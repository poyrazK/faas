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
// Prometheus-derived counters and alerts have independent health and
// observation timestamps. Numeric fallbacks remain stable for older clients;
// signals describes which values are usable. SQL failures surface as 503.
package apidserver

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

// obsHealthHandler composes durable SQL coverage with per-signal telemetry
// health. The Prometheus reads share one deadline in operatorobs.
func (s *server) obsHealthHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	response, prob := s.obsHealthSnapshot(r.Context())
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) obsHealthSnapshot(ctx context.Context) (api.ObsHealthResponse, *api.Problem) {
	missing := seedHealthKindCounts()
	storeCounts, err := s.operatorIntentOutcomeMissing(ctx)
	if err != nil {
		return api.ObsHealthResponse{}, api.ErrCapacity("could not count stuck-running operator intents")
	}
	for kind, n := range storeCounts {
		missing[kind] = n
	}
	ratios := seedHealthKindRatios()
	storeRatios, err := s.traceIDCompletenessRatio(ctx)
	if err != nil {
		return api.ObsHealthResponse{}, api.ErrCapacity("could not compute trace_id completeness ratio")
	}
	for kind, r := range storeRatios {
		ratios[kind] = r
	}
	auditCoverage, err := s.store.OperatorAuditTraceCoverage(ctx, time.Now().UTC().Add(-obsHealthQueryWindow))
	if err != nil {
		return api.ObsHealthResponse{}, api.ErrCapacity("could not compute audit trace coverage")
	}
	enabled := s.promqlClient != nil && s.promqlClient.BaseURL() != ""
	history, retention, err := operatorobs.MaintenanceHealth(ctx, s.store, enabled, time.Now().UTC())
	if err != nil {
		return api.ObsHealthResponse{}, api.ErrCapacity("could not load alert maintenance health")
	}
	telemetry := operatorobs.PrometheusHealth(ctx, s.promqlClient, time.Now().UTC())
	return api.ObsHealthResponse{
		GeneratedAt:                        time.Now().UTC(),
		PrometheusAvailable:                telemetry.Available,
		AuditLogWriteTotal5m:               telemetry.AuditWrites,
		AuditLogWriteFailures5m:            telemetry.AuditFailures,
		AuditLogCoverageRatio5m:            auditCoverage,
		OperatorIntentOutcomeMissingCounts: missing,
		TraceIDCompletenessRatio:           ratios,
		AlertsFiring:                       telemetry.AlertsFiring,
		Signals:                            telemetry.Signals,
		AlertHistory:                       &history,
		AlertRetention:                     &retention,
	}, nil
}
