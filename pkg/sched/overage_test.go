package sched_test

// overage_test.go (issue #561) pins the OverageChecker seam:
//
//  1. memCacheOverageChecker caches the cap+observed on first call
//     and serves the cached value on second call within TTL.
//  2. memCacheOverageChecker returns OverageReached only when
//     observed >= cap (>= not > so "exactly at the cap" rejects).
//  3. memCacheOverageChecker fails open on a transient store error
//     (returns OverageOK so the wake proceeds; meterd is the safety
//     net for sustained outages).
//  4. memCacheOverageChecker.RecordReached is deduped per UTC day:
//     the second call within the same UTC day does not emit.
//  5. memCacheOverageChecker.Invalidate drops the cached entry so
//     the next Check re-reads.
//  6. memCacheOverageChecker NULL cap (no cap) caches OverageOK so
//     successive wakes skip the round-trip.
//  7. AlwaysOKOverageChecker returns OverageOK always (the test
//     stub shape used by engine_test.go's TestAdmitGate_Outcomes).

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/sched"
	"sync"
	"testing"
	"time"
)

// overageFixture is a small overageReadStore implementation backed by
// pluggable fields so individual tests can pin a specific scenario.
// All three reads are O(1) and synchronous; no goroutine plumbing.
type overageFixture struct {
	mu sync.Mutex

	capCents    *int64 // nil = NULL (no cap)
	observedVal int64
	capErr      error // when non-nil, GetAccountOverageCapCents returns this

	auditEvents []recordedAudit
}

type recordedAudit struct {
	actor   string
	kind    string
	subject string
}

func (f *overageFixture) GetAccountOverageCapCents(_ context.Context, _ string) (int64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.capErr != nil {
		return 0, false, f.capErr
	}
	if f.capCents == nil {
		return 0, false, nil
	}
	return *f.capCents, true, nil
}

func (f *overageFixture) CurrentMonthOverageCents(_ context.Context, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.observedVal, nil
}

func (f *overageFixture) AppendEvent(_ context.Context, actor, kind string, subject *string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	auditSubj := ""
	if subject != nil {
		auditSubj = *subject
	}
	f.auditEvents = append(f.auditEvents, recordedAudit{actor: actor, kind: kind, subject: auditSubj})
	return nil
}

func (f *overageFixture) countReachedToday() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, e := range f.auditEvents {
		if e.kind == "overage.cap_reached" {
			n++
		}
	}
	return n
}

// ptr is a small helper so tests can construct *int64 literals tersely.
func ptr(v int64) *int64 { return &v }

func TestOverageChecker_CachesThenExpires(t *testing.T) {
	cap := ptr(int64(100))
	f := &overageFixture{capCents: cap, observedVal: 200}

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)
	(*c.Now()) = func() time.Time { return now }

	ctx := context.Background()
	status, obs, capCents, err := c.Check(ctx, "acct-1")
	if err != nil {
		t.Fatalf("first Check: %v", err)
	}
	if status != sched.OverageReached {
		t.Fatalf("first Check: got %v, want OverageReached", status)
	}
	if obs != 200 || capCents != 100 {
		t.Fatalf("first Check: got obs=%d cap=%d, want 200/100", obs, capCents)
	}

	// Second call within TTL: same answer.
	now = now.Add(2 * time.Second)
	(*c.Now()) = func() time.Time { return now }
	status, _, _, err = c.Check(ctx, "acct-1")
	if err != nil {
		t.Fatalf("second Check: %v", err)
	}
	if status != sched.OverageReached {
		t.Fatalf("second Check within TTL: got %v, want OverageReached (cache miss?)", status)
	}

	// Clear the cap and Invalidate → next Check must re-read and see OK.
	f.mu.Lock()
	f.capCents = nil
	f.mu.Unlock()
	c.Invalidate("acct-1")
	status, _, _, err = c.Check(ctx, "acct-1")
	if err != nil {
		t.Fatalf("post-Invalidate Check: %v", err)
	}
	if status != sched.OverageOK {
		t.Fatalf("post-Invalidate Check: got %v, want OverageOK (cap cleared)", status)
	}
}

func TestOverageChecker_FailsOpenOnStoreError(t *testing.T) {
	f := &overageFixture{capErr: errors.New("synthetic store error")}
	c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)

	ctx := context.Background()
	status, obs, capCents, err := c.Check(ctx, "acct-x")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if status != sched.OverageOK {
		t.Fatalf("fail-open: got %v, want OverageOK", status)
	}
	if obs != 0 || capCents != 0 {
		t.Fatalf("fail-open: got obs=%d cap=%d, want 0/0", obs, capCents)
	}
}

func TestOverageChecker_DedupePerUTCDay(t *testing.T) {
	cap := ptr(int64(100))
	f := &overageFixture{capCents: cap, observedVal: 150}

	now := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)
	(*c.Now()) = func() time.Time { return now }

	ctx := context.Background()
	// Three RecordReached calls on day 1: only one emits.
	c.RecordReached(ctx, "acct-d", 150, 100)
	c.RecordReached(ctx, "acct-d", 160, 100)
	c.RecordReached(ctx, "acct-d", 170, 100)
	if n := f.countReachedToday(); n != 1 {
		t.Fatalf("day 1: got %d audit rows, want 1", n)
	}

	// Day 2: new emit.
	now = time.Date(2026, 8, 7, 0, 0, 1, 0, time.UTC)
	(*c.Now()) = func() time.Time { return now }
	c.Invalidate("acct-d")
	status, _, _, _ := c.Check(ctx, "acct-d")
	if status != sched.OverageReached {
		t.Fatalf("day 2 Check: got %v, want OverageReached", status)
	}
	c.RecordReached(ctx, "acct-d", 200, 100)
	c.RecordReached(ctx, "acct-d", 210, 100) // dedupe within day 2
	if n := f.countReachedToday(); n != 2 {
		t.Fatalf("day 1+2: got %d audit rows, want 2 (one per UTC day)", n)
	}
}

func TestOverageChecker_NullCapCachesOK(t *testing.T) {
	f := &overageFixture{capCents: nil, observedVal: 0}
	c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)

	ctx := context.Background()
	status, _, _, err := c.Check(ctx, "acct-n")
	if err != nil {
		t.Fatalf("NULL cap Check: %v", err)
	}
	if status != sched.OverageOK {
		t.Fatalf("NULL cap Check: got %v, want OverageOK", status)
	}

	// Now plant a cap and move the clock past TTL — the next read must
	// see the new cap.
	f.mu.Lock()
	f.capCents = ptr(int64(0))
	f.mu.Unlock()
	(*c.Now()) = func() time.Time { return time.Now().Add(time.Hour) }
	status, _, capCents, err := c.Check(ctx, "acct-n")
	if err != nil {
		t.Fatalf("post-TTL Check: %v", err)
	}
	if status != sched.OverageReached {
		t.Fatalf("post-TTL re-Read with cap=0: got %v, want OverageReached (proves cache expired)", status)
	}
	if capCents != 0 {
		t.Fatalf("post-TTL Check: got cap=%d, want 0 (cap=0 means no overage allowed)", capCents)
	}
}

func TestOverageChecker_ObserveAtCapEqualBoundary(t *testing.T) {
	// Pin the >= boundary: observed == cap is a refusal (matches the
	// spec wording "meets or exceeds"). observed < cap → OK.
	for _, tc := range []struct {
		name              string
		capCents          int64
		observedCents     int64
		wantOverageStatus sched.OverageStatus
	}{
		{"below cap", 100, 99, sched.OverageOK},
		{"at cap", 100, 100, sched.OverageReached},
		{"above cap", 100, 101, sched.OverageReached},
		{"cap=0, observed=0", 0, 0, sched.OverageReached},
		{"cap=0, observed=1", 0, 1, sched.OverageReached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &overageFixture{capCents: ptr(tc.capCents), observedVal: tc.observedCents}
			c := sched.XNewMemCacheOverageChecker(f, 5*time.Second)
			status, _, _, err := c.Check(context.Background(), "acct-b")
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if status != tc.wantOverageStatus {
				t.Fatalf("got %v, want %v", status, tc.wantOverageStatus)
			}
		})
	}
}

func TestAlwaysOKOverageChecker_StubShape(t *testing.T) {
	checker := sched.AlwaysOKOverageChecker()
	status, _, _, err := checker.Check(context.Background(), "any-account")
	if err != nil || status != sched.OverageOK {
		t.Fatalf("Check: got (%v, %v), want (OverageOK, nil)", status, err)
	}
	// RecordReached / Invalidate are no-ops (must not panic).
	checker.RecordReached(context.Background(), "any-account", 100, 50)
	checker.Invalidate("any-account")
}
