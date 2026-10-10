package main

import (
	"context"
	"errors"
	"testing"
	"time"

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
