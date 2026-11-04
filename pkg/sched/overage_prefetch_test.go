package sched_test

// adr: 20261103164725 — the reaper reads a tick's spend caps in one query
// and accepts a status up to 30 s old; the wake gate keeps its 5 s.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/sched"
)

type capsFixture struct {
	caps       map[string]int64 // absent = no cap
	observed   int64
	bulkReads  int
	capReads   int
	monthReads int
}

func (f *capsFixture) GetAccountOverageCapCents(_ context.Context, id string) (int64, bool, error) {
	f.capReads++
	c, ok := f.caps[id]
	return c, ok, nil
}

func (f *capsFixture) CurrentMonthOverageCents(context.Context, string) (int64, error) {
	f.monthReads++
	return f.observed, nil
}

func (f *capsFixture) AppendEvent(context.Context, string, string, *string, []byte) error { return nil }

func (f *capsFixture) AccountOverageCaps(_ context.Context, ids []string) (map[string]int64, error) {
	f.bulkReads++
	out := map[string]int64{}
	for _, id := range ids {
		if c, ok := f.caps[id]; ok {
			out[id] = c
		}
	}
	return out, nil
}

func TestOverageCheckerPrefetchAndCheckWithin(t *testing.T) {
	ctx := context.Background()
	f := &capsFixture{caps: map[string]int64{"capped": 100}, observed: 200}
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)
	*c.Now() = func() time.Time { return now }
	const maxAge = 30 * time.Second
	ids := []string{"free-1", "free-2", "capped", "free-1"}

	c.Prefetch(ctx, ids, maxAge)
	if f.bulkReads != 1 {
		t.Fatalf("bulk cap reads = %d, want 1", f.bulkReads)
	}
	for _, id := range []string{"free-1", "free-2"} {
		if status, _, _, err := c.CheckWithin(ctx, id, maxAge); err != nil || status != sched.OverageOK {
			t.Fatalf("CheckWithin(%s) = %v (err %v), want OK", id, status, err)
		}
	}
	if f.capReads != 0 || f.monthReads != 0 {
		t.Fatalf("uncapped accounts read the store: %d cap, %d month reads", f.capReads, f.monthReads)
	}
	if status, _, _, _ := c.CheckWithin(ctx, "capped", maxAge); status != sched.OverageReached || f.monthReads != 1 {
		t.Fatalf("CheckWithin(capped) = %v after %d month reads, want reached after 1", status, f.monthReads)
	}

	// 20 s on: the checker's 5 s TTL has passed, the reaper's 30 s has not.
	now = now.Add(20 * time.Second)
	c.Prefetch(ctx, ids, maxAge)
	if status, _, _, _ := c.CheckWithin(ctx, "capped", maxAge); status != sched.OverageReached || f.bulkReads != 1 || f.monthReads != 1 {
		t.Fatalf("within 30 s: status %v, %d bulk and %d month reads; want the cached status and no reads", status, f.bulkReads, f.monthReads)
	}
	if _, _, _, _ = c.Check(ctx, "capped"); f.monthReads != 2 {
		t.Fatalf("the wake gate's Check made %d month reads, want a fresh one past its 5 s", f.monthReads)
	}

	now = now.Add(40 * time.Second)
	c.Prefetch(ctx, ids, maxAge)
	if f.bulkReads != 2 {
		t.Fatalf("bulk cap reads = %d after 30 s, want 2", f.bulkReads)
	}
}
