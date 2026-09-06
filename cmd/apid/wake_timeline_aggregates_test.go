package main

// Regression tests for aggregateWakeTimeline (review-fix cluster,
// commit 3 of PR #1097). The helper is shared between the HTML
// page handler (cmd/apid/handlers_dashboard.go::renderAppWakeTimeline)
// and the JSON mirror handler
// (cmd/apid/handlers_app_wake_timeline_json.go::buildAppWakeTimeline).
//
// The invariants tested here are the load-bearing ones from the
// PR-A review cluster (PR #1031 findings #4 + #5):

//   - Descending-cutoff break: the first row with
//     started_at < cutoff ends iteration; rows past it are
//     never counted.
//   - Two-denominator rule: rows without boot-meta
//     contribute to WakeCount24h but NOT to
//     WakeCountWithMeta (the at-cap % denominator).
//   - AtCapacityCount requires all three: hasMeta AND
//     AtCapacityPresent AND AtCapacity.
//   - Trigger histogram excludes rows with empty trigger
//     (treated as "unknown", not bucketed).
//
// Test surface is the helper itself, not the HTTP layer, so the
// pins survive any future refactor that changes the wire row
// shape (the JSON mirror and the HTML page emit different row
// types — the helper's invariants are independent of row type).

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestAggregateWakeTimeline_DescendingCutoffBreak pins the
// load-bearing invariant from PR-A review cluster finding #4:
// the moment a row's started_at falls before the cutoff, the
// loop breaks. Rows past the cutoff are NEVER counted, even if
// they appear earlier in the DESC slice.
func TestAggregateWakeTimeline_DescendingCutoffBreak(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-24 * time.Hour)
	instances := []state.Instance{
		{ID: "newest", StartedAt: now.Add(-1 * time.Hour), WakeID: "w-newest"},
		{ID: "recent", StartedAt: now.Add(-12 * time.Hour), WakeID: "w-recent"},
		{ID: "old-pre-cutoff", StartedAt: now.Add(-50 * time.Hour), WakeID: "w-old"},
	}
	metas := map[string]state.WakeBootMeta{
		"w-newest": {Trigger: "http", AtCapacityPresent: true, AtCapacity: false},
		"w-recent": {Trigger: "http", AtCapacityPresent: true, AtCapacity: true},
		"w-old":    {Trigger: "http", AtCapacityPresent: true, AtCapacity: true},
	}
	agg := aggregateWakeTimeline(instances, metas, cutoff)
	if agg.WakeCount24h != 2 {
		t.Errorf("WakeCount24h = %d, want 2 (pre-cutoff row must be excluded)", agg.WakeCount24h)
	}
	if agg.WakeCountWithMeta != 2 {
		t.Errorf("WakeCountWithMeta = %d, want 2", agg.WakeCountWithMeta)
	}
	if agg.AtCapacityCount != 1 {
		t.Errorf("AtCapacityCount = %d, want 1 (only the recent row was at-cap)", agg.AtCapacityCount)
	}
}

// TestAggregateWakeTimeline_TwoDenominatorRule pins finding #5:
// rows without boot-meta (pre-ADR-123 fleet) contribute to
// WakeCount24h but NOT to WakeCountWithMeta. The at-cap % must
// be computed against WakeCountWithMeta, not WakeCount24h, so
// pre-ADR-123 customers don't see a misleading 0% on a fleet
// where half their wakes lack metadata.
func TestAggregateWakeTimeline_TwoDenominatorRule(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-24 * time.Hour)
	instances := []state.Instance{
		{ID: "meta-1", StartedAt: now.Add(-1 * time.Hour), WakeID: "w1"},
		{ID: "meta-2", StartedAt: now.Add(-2 * time.Hour), WakeID: "w2"},
		{ID: "no-meta-1", StartedAt: now.Add(-3 * time.Hour), WakeID: "w3"}, // no entry in metas
		{ID: "no-meta-2", StartedAt: now.Add(-4 * time.Hour), WakeID: "w4"}, // no entry in metas
	}
	metas := map[string]state.WakeBootMeta{
		"w1": {Trigger: "http", AtCapacityPresent: true, AtCapacity: true},
		"w2": {Trigger: "cron", AtCapacityPresent: true, AtCapacity: false},
	}
	agg := aggregateWakeTimeline(instances, metas, cutoff)
	if agg.WakeCount24h != 4 {
		t.Errorf("WakeCount24h = %d, want 4 (all rows in window)", agg.WakeCount24h)
	}
	if agg.WakeCountWithMeta != 2 {
		t.Errorf("WakeCountWithMeta = %d, want 2 (only meta-bearing rows)", agg.WakeCountWithMeta)
	}
	// 1 of 2 at-cap = 50%.
	want := 50.0
	if agg.AtCapacityPct != want {
		t.Errorf("AtCapacityPct = %v, want %v (denominator is WakeCountWithMeta, not WakeCount24h)", agg.AtCapacityPct, want)
	}
}

// TestAggregateWakeTimeline_AtCapacityRequiresAllThree pins the
// at-cap increment rule: AtCapacityCount advances ONLY when all
// three conditions hold. The AtCapacityPresent flag exists
// precisely to distinguish "explicit false" from "absent" (the
// em-dash case on the wire shape).
func TestAggregateWakeTimeline_AtCapacityRequiresAllThree(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-24 * time.Hour)
	instances := []state.Instance{
		{ID: "1", StartedAt: now.Add(-1 * time.Hour), WakeID: "w1"},
		{ID: "2", StartedAt: now.Add(-2 * time.Hour), WakeID: "w2"},
		{ID: "3", StartedAt: now.Add(-3 * time.Hour), WakeID: "w3"},
	}
	metas := map[string]state.WakeBootMeta{
		// All three present and true → counted.
		"w1": {Trigger: "http", AtCapacityPresent: true, AtCapacity: true},
		// AtCapacityPresent true but AtCapacity false → NOT counted.
		"w2": {Trigger: "http", AtCapacityPresent: true, AtCapacity: false},
		// AtCapacityPresent false (absent) → NOT counted even
		// though AtCapacity is true (pre-PR-A fleet).
		"w3": {Trigger: "http", AtCapacityPresent: false, AtCapacity: true},
	}
	agg := aggregateWakeTimeline(instances, metas, cutoff)
	if agg.AtCapacityCount != 1 {
		t.Errorf("AtCapacityCount = %d, want 1 (only w1)", agg.AtCapacityCount)
	}
}

// TestAggregateWakeTimeline_TriggerHistogramExcludesEmpty pins
// the trigger histogram filter: an empty Trigger string is
// treated as "unknown" and not bucketed. The renderTriggerHistogram
// HTML chip and the JSON mirror's TriggerHistogram field both
// inherit this contract.
func TestAggregateWakeTimeline_TriggerHistogramExcludesEmpty(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-24 * time.Hour)
	instances := []state.Instance{
		{ID: "1", StartedAt: now.Add(-1 * time.Hour), WakeID: "w1"},
		{ID: "2", StartedAt: now.Add(-2 * time.Hour), WakeID: "w2"},
		{ID: "3", StartedAt: now.Add(-3 * time.Hour), WakeID: "w3"},
	}
	metas := map[string]state.WakeBootMeta{
		"w1": {Trigger: "http"},
		"w2": {Trigger: "cron"},
		"w3": {Trigger: ""}, // excluded from histogram
	}
	agg := aggregateWakeTimeline(instances, metas, cutoff)
	if got := agg.TriggerHistogram["http"]; got != 1 {
		t.Errorf("TriggerHistogram[http] = %d, want 1", got)
	}
	if got := agg.TriggerHistogram["cron"]; got != 1 {
		t.Errorf("TriggerHistogram[cron] = %d, want 1", got)
	}
	if got, ok := agg.TriggerHistogram[""]; ok {
		t.Errorf("TriggerHistogram[\"\"] = %d, want absent (empty trigger must not bucket)", got)
	}
}

// TestAggregateWakeTimeline_NilSliceReturnsEmptyMap pins the
// wire-shape contract: a nil input slice returns a non-nil empty
// TriggerHistogram (not nil) so the JSON encoder emits `{}` on
// the wire rather than `null`. AppWakeTimelineResponse doc
// comment in pkg/api/dto.go explicitly calls this out.
func TestAggregateWakeTimeline_NilSliceReturnsEmptyMap(t *testing.T) {
	agg := aggregateWakeTimeline(nil, nil, time.Now().UTC())
	if agg.TriggerHistogram == nil {
		t.Errorf("TriggerHistogram is nil — wire-shape contract requires non-nil empty map")
	}
	if len(agg.TriggerHistogram) != 0 {
		t.Errorf("TriggerHistogram not empty: %+v", agg.TriggerHistogram)
	}
	if agg.WakeCount24h != 0 || agg.WakeCountWithMeta != 0 || agg.AtCapacityCount != 0 || agg.AtCapacityPct != 0 {
		t.Errorf("non-zero counters on nil input: %+v", agg)
	}
}

// TestAggregateWakeTimeline_DivisionByZeroGuard pins the
// at-cap % zero-denominator behaviour: an empty WakeCountWithMeta
// must NOT panic on division by zero and must produce 0% on the
// wire. Both the HTML page and the JSON mirror rely on this.
func TestAggregateWakeTimeline_DivisionByZeroGuard(t *testing.T) {
	now := time.Now().UTC()
	cutoff := now.Add(-24 * time.Hour)
	// Rows present but none have meta → WakeCountWithMeta = 0.
	instances := []state.Instance{
		{ID: "1", StartedAt: now.Add(-1 * time.Hour), WakeID: "w1"},
	}
	agg := aggregateWakeTimeline(instances, map[string]state.WakeBootMeta{}, cutoff)
	if agg.WakeCount24h != 1 {
		t.Errorf("WakeCount24h = %d, want 1", agg.WakeCount24h)
	}
	if agg.WakeCountWithMeta != 0 {
		t.Errorf("WakeCountWithMeta = %d, want 0", agg.WakeCountWithMeta)
	}
	if agg.AtCapacityPct != 0 {
		t.Errorf("AtCapacityPct = %v, want 0 (denominator is 0; must not divide)", agg.AtCapacityPct)
	}
}

func TestWakeTimelineWindow_ExplicitBounds(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	instances := []state.Instance{
		{ID: "future", StartedAt: now.Add(time.Hour), WakeID: "future"},
		{ID: "new", StartedAt: now, WakeID: "new"},
		{ID: "old", StartedAt: now.Add(-48 * time.Hour), WakeID: "old"},
		{ID: "older", StartedAt: now.Add(-72 * time.Hour), WakeID: "older"},
	}
	rows := wakeTimelineWindow(instances, now.Add(-24*time.Hour), now)
	if len(rows) != 1 || rows[0].WakeID != "new" {
		t.Fatalf("window rows = %#v, want only new row", rows)
	}
}
