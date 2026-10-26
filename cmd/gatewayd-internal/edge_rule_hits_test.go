package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type failingHitStore struct {
	*state.MemStore
	fail bool
}

func (f *failingHitStore) RecordEdgeRuleHits(ctx context.Context, hits []state.EdgeRuleHit) error {
	if f.fail {
		return errors.New("postgres unavailable")
	}
	return f.MemStore.RecordEdgeRuleHits(ctx, hits)
}

// ADR-960: hits accumulate in memory and reach the store on flush; a failed
// flush keeps the counts for the next attempt instead of losing them.
func TestEdgeRuleHitCounterFlushesAndRetains(t *testing.T) {
	store := &failingHitStore{MemStore: state.NewMemStore(), fail: true}
	c := newEdgeRuleHitCounter()
	c.now = func() time.Time { return time.Date(2026, 10, 9, 12, 30, 0, 0, time.UTC) }
	for range 3 {
		c.RecordEdgeRuleHit("11111111-1111-1111-1111-111111111111", "app-1", false)
	}
	c.RecordEdgeRuleHit("22222222-2222-2222-2222-222222222222", "app-1", true)

	c.flush(context.Background(), store, nil)
	stats, _ := store.EdgeRuleHitStatsForApp(context.Background(), "app-1", time.Time{})
	if len(stats) != 0 {
		t.Fatalf("failed flush wrote stats: %+v", stats)
	}

	store.fail = false
	c.RecordEdgeRuleHit("11111111-1111-1111-1111-111111111111", "app-1", false)
	c.flush(context.Background(), store, nil)
	stats, _ = store.EdgeRuleHitStatsForApp(context.Background(), "app-1", time.Time{})
	got := map[string]state.EdgeRuleHitStats{}
	for _, s := range stats {
		got[s.RuleID] = s
	}
	if got["11111111-1111-1111-1111-111111111111"].Matched != 4 || got["22222222-2222-2222-2222-222222222222"].Logged != 1 {
		t.Fatalf("stats after retry = %+v, want matched 4 and logged 1", stats)
	}

	c.flush(context.Background(), store, nil)
	again, _ := store.EdgeRuleHitStatsForApp(context.Background(), "app-1", time.Time{})
	if len(again) != 2 || again[0].Matched+again[1].Matched != 4 {
		t.Fatalf("an empty flush changed totals: %+v", again)
	}
}

// ADR-831 amendment 5: detections have their own per-rule budget, reset each
// flush, and reach the store with their outcome and WAF detail.
func TestEdgeRuleHitCounterDetectionEvents(t *testing.T) {
	c := newEdgeRuleHitCounter()
	// Match sampling never spends the detection budget.
	c.RecordEdgeRuleHit("waf-rule", "app", false)
	for range edgeRuleEventsPerRule + 5 {
		c.SampleEdgeRuleEvent("waf-rule")
	}
	admitted := 0
	for range edgeRuleDetectionEventsPerRule + 5 {
		if c.SampleEdgeRuleDetection("waf-rule") {
			admitted++
		}
	}
	if admitted != edgeRuleDetectionEventsPerRule {
		t.Fatalf("admitted %d detections, want %d", admitted, edgeRuleDetectionEventsPerRule)
	}
	if !c.SampleEdgeRuleDetection("other-rule") {
		t.Fatal("another rule's detection was refused")
	}
	c.RecordEdgeRuleEvent(gateway.EdgeRuleEvent{RuleID: "waf-rule", AppID: "app", Outcome: gateway.EdgeRuleEventBlocked,
		WAFRuleIDs: []int{942100}, WAFCategories: []string{"sqli"}, WAFScore: 5})
	c.RecordEdgeRuleEvent(gateway.EdgeRuleEvent{RuleID: "r", AppID: "app", Logged: true})
	events, _ := c.drainEvents()
	if len(events) != 2 || events[0].Outcome != state.EdgeRuleEventBlocked || events[0].WAFScore != 5 ||
		events[0].WAFRuleIDs[0] != 942100 || events[1].Outcome != state.EdgeRuleHitLogged {
		t.Fatalf("drained = %+v", events)
	}
	if !c.SampleEdgeRuleDetection("waf-rule") {
		t.Fatal("detection budget not reset by the flush")
	}
}

// ADR-20261022105846 amendment 2: penalty box events have a per-rule budget
// of their own, untouched by detections and reset each flush, and reach the
// store as boxed, an outcome its CHECK accepts.
func TestEdgeRuleHitCounterPenaltyBoxEvents(t *testing.T) {
	var _ gateway.PenaltyEventRecorder = (*edgeRuleHitCounter)(nil)
	c := newEdgeRuleHitCounter()
	for range edgeRuleDetectionEventsPerRule + 5 {
		c.SampleEdgeRuleDetection("rule")
	}
	admitted := 0
	for range edgeRulePenaltyEventsPerRule + 5 {
		if c.SampleEdgePenaltyBox("rule") {
			admitted++
		}
	}
	if admitted != edgeRulePenaltyEventsPerRule {
		t.Fatalf("admitted %d boxes, want %d", admitted, edgeRulePenaltyEventsPerRule)
	}
	c.RecordEdgeRuleEvent(gateway.EdgeRuleEvent{RuleID: "rule", AppID: "app", Outcome: gateway.EdgeRuleEventBoxed, ClientIP: "203.0.113.7"})
	events, _ := c.drainEvents()
	if len(events) != 1 || events[0].Outcome != state.EdgeRuleEventBoxed || events[0].ClientIP != "203.0.113.7" {
		t.Fatalf("drained = %+v", events)
	}
	if !c.SampleEdgePenaltyBox("rule") {
		t.Fatal("penalty box budget not reset by the flush")
	}
	if gateway.EdgeRuleEventBoxed != state.EdgeRuleEventBoxed || !state.IsEdgeRuleEventOutcome(gateway.EdgeRuleEventBoxed) {
		t.Fatalf("gateway outcome %q is not the store's", gateway.EdgeRuleEventBoxed)
	}
}

// The gateway and the store name detection outcomes independently; the
// stored CHECK accepts only the store's names.
func TestDetectionOutcomesMatchStore(t *testing.T) {
	for gw, st := range map[string]string{
		gateway.EdgeRuleEventDetected: state.EdgeRuleEventDetected,
		gateway.EdgeRuleEventWarned:   state.EdgeRuleEventWarned,
		gateway.EdgeRuleEventBlocked:  state.EdgeRuleEventBlocked,
	} {
		if gw != st {
			t.Errorf("gateway outcome %q != store outcome %q", gw, st)
		}
	}
}
