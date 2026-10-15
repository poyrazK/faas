// SQL coverage and stuck-intent helpers for GET /v1/admin/obs/health.
// Prometheus values and their freshness evidence live in operatorobs.
package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// obsHealthQueryWindow is the trailing window for every PromQL +
// SQL aggregation in /v1/admin/obs/health. 5m matches the
// existing /v1/admin/obs/audit-log/search window and the C5
// gauge tick interval (60s); the operator can still scrape this
// endpoint on a 30s cadence and see smooth tiles.
const obsHealthQueryWindow = api.ObsHealthWindow

// obsHealthStuckRunningThreshold is the cutoff for the
// operator_intent_outcome_missing_total tile: a row is "stuck"
// when it has been running for longer than this. Mirrors the
// schedd-side operatorIntentStuckRunningTimeout (5m, default)
// so the operator's view matches schedd's reclaim horizon —
// schedd will reclaim a row at exactly the threshold the
// operator sees it as "missing" at.
const obsHealthStuckRunningThreshold = 5 * time.Minute

// operatorIntentOutcomeMissing returns the per-kind count of
// stuck-running operator_intents rows. The handler seeds
// zero-count kinds from obsHealthKindVocabulary so the JSON
// shape is stable. On store error the handler surfaces 503 —
// this method only returns the partial result it managed to
// read.
func (s *server) operatorIntentOutcomeMissing(ctx context.Context) (map[string]int, error) {
	threshold := time.Now().UTC().Add(-obsHealthStuckRunningThreshold)
	return s.store.OperatorIntentOutcomeMissingCounts(ctx, threshold)
}

// traceIDCompletenessRatio returns the per-kind ratio of
// operator.action.* events with non-NULL trace_id. Reads from
// the events table (live, AppendEventWithTrace writer). The
// handler seeds zero-row kinds to 1.0 (vacuous truth) per the
// Store interface comment.
func (s *server) traceIDCompletenessRatio(ctx context.Context) (map[string]float64, error) {
	since := time.Now().UTC().Add(-obsHealthQueryWindow)
	return s.store.OperatorActionTraceCompleteness(ctx, since)
}

// seedHealthKindCounts returns the closed-set seed map for
// counts: every kind in api.ObsHealthKindVocabulary maps to 0 so
// a fresh deploy with no operator actions yet still reports
// "force_park: 0, force_cold_boot: 0, force_restart: 0" rather
// than an empty map. The handler overlays the SQL result on
// top of this seed.
//
// Used by both the handler and the test file (via exported
// name) — the test file pins the seed shape via
// TestObsHealthHandler_StableJSONShapeOnEmptyDB.
func seedHealthKindCounts() map[string]int {
	out := make(map[string]int, len(api.ObsHealthKindVocabulary))
	for _, k := range api.ObsHealthKindVocabulary {
		out[k] = 0
	}
	return out
}

// seedHealthKindRatios mirrors seedHealthKindCounts for the
// ratios map. Same closed-set seed; the per-kind ratio
// (vacuous truth 1.0) is the right default for an empty
// window.
func seedHealthKindRatios() map[string]float64 {
	out := make(map[string]float64, len(api.ObsHealthKindVocabulary))
	for _, k := range api.ObsHealthKindVocabulary {
		out[k] = 1.0
	}
	return out
}
