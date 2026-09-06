// Shared wake-timeline aggregation primitives for the per-app
// dashboard (review-fix cluster, commit 3 of PR #1097).
//
// The load-bearing invariants of the customer-facing wake-timeline
// view — the 24h cutoff descending-break (PR-A review cluster
// finding #4, PR #1031) and the two-denominator rule for
// at_capacity_pct (PR-A review cluster finding #5, PR #1031) —
// were previously duplicated verbatim across two handlers:
//
//   - cmd/apid/handlers_dashboard.go::renderAppWakeTimeline
//     (HTML page at /apps/{slug}/wake-timeline)
//   - cmd/apid/handlers_app_wake_timeline_json.go::buildAppWakeTimeline
//     (JSON mirror at /v1/apps/{slug}/wake-timeline)
//
// The original duplication was deliberate ("don't refactor
// working code without a failing test") but became a drift
// hazard once a second caller landed — when PR-A round-3
// widens the cutoff window or changes the at-cap denominator
// definition, the JSON mirror would silently keep the old
// semantics. Code-review on PR #1097 surfaced this as the
// highest-priority simplification.
//
// This helper extracts the counter math into a single
// source-of-truth. The HTML page still emits
// `views.WakeTimelineRow` and the JSON mirror still emits
// `api.WakeTimelineJSONRow` — the row shapes are genuinely
// different (the JSON contract has additional boolean
// sentinels like `at_capacity_present` and an em-dash policy
// on `ready_in_ms` that the HTML template doesn't need).
// Each call-site iterates the instances slice itself and
// reads the counter accumulators from the helper return.
//
// Helper-level invariants pinned by
// TestWakeTimelineAggregates_* (see
// wake_timeline_aggregates_test.go):

//   - Iteration order matches the input slice (DESC, caller
//     responsibility — the SQL reader guarantees the order).
//   - First row with started_at < cutoff triggers break.
//   - Rows with hasMeta=false do NOT contribute to
//     wakeCountWithMeta (they DO contribute to wakeCount24h).
//   - atCapCount is incremented only when hasMeta AND
//     at_capacity_present AND at_capacity are all true.
//   - Trigger histogram counts are filtered on non-empty
//     trigger name (an empty trigger string is treated as
//     "unknown" and not bucketed — same posture as the
//     renderTriggerHistogram HTML chip).

package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// wakeTimelineAggregates is the counter rollup for one
// wake-timeline render. The fields are the load-bearing
// invariants the HTML and JSON views share:
//
//   - WakeCount24h: total rows in the trailing-24h window
//     (post-cutoff break).
//   - WakeCountWithMeta: denominator for AtCapacityPct —
//     rows where the wake-boot telemetry lookup succeeded.
//   - AtCapacityCount: rows in WakeCountWithMeta where the
//     admit was at-capacity (the at_capacity_present flag is
//     set AND at_capacity is true).
//   - AtCapacityPct: AtCapacityCount / WakeCountWithMeta ×
//     100, or 0 if WakeCountWithMeta is 0 (no division-by-zero
//     in the wire path).
//   - TriggerHistogram: per-trigger counts across the rows in
//     WakeCountWithMeta (pre-ADR-123 fleet renders this as an
//     empty map — never nil — the wire-shape contract is
//     "non-nil empty map", see AppWakeTimelineResponse doc).
type wakeTimelineAggregates struct {
	WakeCount24h      int
	WakeCountWithMeta int
	AtCapacityCount   int
	AtCapacityPct     float64
	TriggerHistogram  map[string]int
}

// aggregateWakeTimeline walks the instances slice and computes
// the wake-timeline counter rollup. The slice is expected in
// DESC started_at order (the SQL reader's contract). The
// trailing cutoff defaults to 24h — callers that need a
// different window pass an explicit cutoff, but the customer-
// facing surface uses the 24h default everywhere.
//
// Each caller's iteration of the slice yields the same row
// sequence as the helper's — the helper returns when the
// first pre-cutoff row is seen (descending-break), but the
// caller must perform the same break in its own row-build
// loop. The helper does NOT pre-compute the cutoff so the
// caller can derive it from the same `now` instant both
// sites share (avoiding a race where one site uses now+1ms
// and the other uses now+2ms).
//
// Side effect: the helper does not mutate the input slice
// (read-only). Safe for concurrent use — no shared state.
//
// Failure modes: nil input slice returns the zero aggregates
// with a non-nil empty TriggerHistogram (matches the wire-
// shape contract — see AppWakeTimelineResponse.TriggerHistogram
// doc comment in pkg/api/dto.go).
func aggregateWakeTimeline(instances []state.Instance, bootMetas map[string]state.WakeBootMeta, cutoff time.Time) wakeTimelineAggregates {
	agg := wakeTimelineAggregates{
		TriggerHistogram: make(map[string]int),
	}
	for _, ins := range instances {
		if !ins.StartedAt.IsZero() && ins.StartedAt.UTC().Before(cutoff) {
			// DESC order — first pre-cutoff row means we're done.
			break
		}
		agg.WakeCount24h++
		meta, hasMeta := bootMetas[ins.WakeID]
		if !hasMeta {
			continue
		}
		agg.WakeCountWithMeta++
		if meta.Trigger != "" {
			agg.TriggerHistogram[meta.Trigger]++
		}
		if meta.AtCapacityPresent && meta.AtCapacity {
			agg.AtCapacityCount++
		}
	}
	if agg.WakeCountWithMeta > 0 {
		agg.AtCapacityPct = float64(agg.AtCapacityCount) / float64(agg.WakeCountWithMeta) * 100
	}
	return agg
}
