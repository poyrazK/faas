package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

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
		c.RecordEdgeRuleHit("11111111-1111-1111-1111-111111111111", fixtureApp, false)
	}
	c.RecordEdgeRuleHit("22222222-2222-2222-2222-222222222222", fixtureApp, true)

	c.flush(context.Background(), store, nil)
	stats, _ := store.EdgeRuleHitStatsForApp(context.Background(), fixtureApp, time.Time{})
	if len(stats) != 0 {
		t.Fatalf("failed flush wrote stats: %+v", stats)
	}

	store.fail = false
	c.RecordEdgeRuleHit("11111111-1111-1111-1111-111111111111", fixtureApp, false)
	c.flush(context.Background(), store, nil)
	stats, _ = store.EdgeRuleHitStatsForApp(context.Background(), fixtureApp, time.Time{})
	got := map[string]state.EdgeRuleHitStats{}
	for _, s := range stats {
		got[s.RuleID] = s
	}
	if got["11111111-1111-1111-1111-111111111111"].Matched != 4 || got["22222222-2222-2222-2222-222222222222"].Logged != 1 {
		t.Fatalf("stats after retry = %+v, want matched 4 and logged 1", stats)
	}

	c.flush(context.Background(), store, nil)
	again, _ := store.EdgeRuleHitStatsForApp(context.Background(), fixtureApp, time.Time{})
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

const fixtureApp = "33333333-3333-3333-3333-333333333333"

// uuidCheckingHitStore fails a whole batch when any id is not a UUID, as the
// Postgres uuid[] casts do.
type uuidCheckingHitStore struct {
	mem *state.MemStore
}

func batchIDsError(ids ...string) error {
	for _, id := range ids {
		if !isAppID(id) {
			return errors.New(`ERROR: invalid input syntax for type uuid: "` + id + `" (SQLSTATE 22P02)`)
		}
	}
	return nil
}

func (s uuidCheckingHitStore) RecordEdgeRuleHits(ctx context.Context, hits []state.EdgeRuleHit) error {
	for _, h := range hits {
		if err := batchIDsError(h.RuleID, h.AppID); err != nil {
			return err
		}
	}
	return s.mem.RecordEdgeRuleHits(ctx, hits)
}

func (s uuidCheckingHitStore) EdgeRuleHitStatsForApp(ctx context.Context, app string, since time.Time) ([]state.EdgeRuleHitStats, error) {
	return s.mem.EdgeRuleHitStatsForApp(ctx, app, since)
}

func (s uuidCheckingHitStore) EdgeRuleHitSeriesForApp(ctx context.Context, app string, since time.Time) ([]state.EdgeRuleHit, error) {
	return s.mem.EdgeRuleHitSeriesForApp(ctx, app, since)
}

func (s uuidCheckingHitStore) PruneEdgeRuleHitCounts(ctx context.Context, before time.Time) (int64, error) {
	return s.mem.PruneEdgeRuleHitCounts(ctx, before)
}

func (s uuidCheckingHitStore) RecordEdgeRuleEvents(ctx context.Context, events []state.EdgeRuleEvent) error {
	for _, e := range events {
		if err := batchIDsError(e.RuleID, e.AppID); err != nil {
			return err
		}
	}
	return s.mem.RecordEdgeRuleEvents(ctx, events)
}

func (s uuidCheckingHitStore) ListEdgeRuleEvents(ctx context.Context, q state.EdgeRuleEventQuery) ([]state.EdgeRuleEvent, error) {
	return s.mem.ListEdgeRuleEvents(ctx, q)
}

func (s uuidCheckingHitStore) PruneEdgeRuleEvents(ctx context.Context, before time.Time) (int64, error) {
	return s.mem.PruneEdgeRuleEvents(ctx, before)
}

// rc.296 recorded hits and events with an empty app id, and the single bad
// row failed every flush: counts retried forever and events were dropped.
// A row whose rule or app id is not a UUID is now skipped and counted; the
// rest of the batch is written.
func TestEdgeRuleHitCounterSkipsRowsWithInvalidIDs(t *testing.T) {
	const good = "44444444-4444-4444-4444-444444444444"
	for _, tc := range []struct{ name, rule, app string }{
		{"empty app", "55555555-5555-5555-5555-555555555555", ""},
		{"empty rule", "", fixtureApp},
		{"malformed app", "55555555-5555-5555-5555-555555555555", "app-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := uuidCheckingHitStore{mem: state.NewMemStore()}
			c := newEdgeRuleHitCounter()
			c.RecordEdgeRuleHit(good, fixtureApp, false)
			c.RecordEdgeRuleHit(tc.rule, tc.app, false)
			c.RecordEdgeRuleEvent(gateway.EdgeRuleEvent{RuleID: good, AppID: fixtureApp, At: time.Now()})
			c.RecordEdgeRuleEvent(gateway.EdgeRuleEvent{RuleID: tc.rule, AppID: tc.app, At: time.Now()})
			c.flush(context.Background(), store, nil)

			stats, err := store.EdgeRuleHitStatsForApp(context.Background(), fixtureApp, time.Time{})
			if err != nil || len(stats) != 1 || stats[0].RuleID != good || stats[0].Matched != 1 {
				t.Fatalf("hit stats = %+v, %v; want the valid rule's one hit", stats, err)
			}
			events, err := store.ListEdgeRuleEvents(context.Background(), state.EdgeRuleEventQuery{AppID: fixtureApp, Limit: 10})
			if err != nil || len(events) != 1 || events[0].RuleID != good {
				t.Fatalf("events = %+v, %v; want the valid rule's event", events, err)
			}
			for kind, want := range map[string]float64{"hit": 1, "event": 1} {
				if got := testutil.ToFloat64(c.skipped.WithLabelValues(kind)); got != want {
					t.Fatalf("skipped %s = %v, want %v", kind, got, want)
				}
			}
			// The skipped row is not retried: the next flush writes nothing more.
			c.flush(context.Background(), store, nil)
			if again, _ := store.EdgeRuleHitStatsForApp(context.Background(), fixtureApp, time.Time{}); len(again) != 1 || again[0].Matched != 1 {
				t.Fatalf("second flush changed totals: %+v", again)
			}
		})
	}
}
