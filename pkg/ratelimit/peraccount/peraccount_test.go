package peraccount_test

// Unit tests for pkg/ratelimit/peraccount — the per-account
// token-bucket pool extracted from cmd/apid/grpc_server_request_telemetry.go
// in ADR-127 PR-D.
//
// Coverage:
//   - NewLimiter + SetClock + Take happy path.
//   - Refill accrues over time when tokens are exhausted.
//   - bucketCap <= 0 always rate-limits.
//   - CacheLimits / CachedLimits honor the 60s TTL.
//   - Bucket capacity caps the refill so a long-idle customer
//     doesn't get a runaway token balance.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
)

// fakeClock returns a function suitable for SetClock that advances
// by `step` per call. Returns the clock + an `Advance` helper.
func fakeClock(start time.Time, step time.Duration) (now func() time.Time, advance func()) {
	t := start
	return func() time.Time { return t }, func() { t = t.Add(step) }
}

// TestLimiter_Take_HappyPath verifies the basic decrement shape:
// 60 tokens, take 3, see 3 successes.
func TestLimiter_Take_HappyPath(t *testing.T) {
	r := peraccount.NewLimiter()
	id := uuid.New()
	for i := 0; i < 3; i++ {
		ok, retryMs := r.Take(id, 60)
		if !ok || retryMs != 0 {
			t.Fatalf("Take #%d: ok=%v retry=%d, want ok=true retry=0", i+1, ok, retryMs)
		}
	}
}

// TestLimiter_Take_EmptyRateLimitsAll verifies bucketCap <= 0
// short-circuits to "always rate-limited, retry-after 60s".
func TestLimiter_Take_EmptyRateLimitsAll(t *testing.T) {
	r := peraccount.NewLimiter()
	id := uuid.New()
	for i := 0; i < 5; i++ {
		ok, retryMs := r.Take(id, 0)
		if ok {
			t.Fatalf("Take #%d (cap=0): expected rate-limited", i+1)
		}
		if retryMs != 60_000 {
			t.Errorf("Take #%d retry=%d, want 60_000", i+1, retryMs)
		}
	}
}

// TestLimiter_Take_RefillAfterTime verifies that an exhausted
// bucket refills at bucketCap / 60 per second. With cap=60 the
// refill is 1 token/sec; advancing the fake clock by 2s should
// allow 2 more takes.
func TestLimiter_Take_RefillAfterTime(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now, advance := fakeClock(start, time.Second)
	r := peraccount.NewLimiter()
	r.SetClock(now)

	id := uuid.New()
	// Drain the bucket (cap=60).
	for i := 0; i < 60; i++ {
		if ok, _ := r.Take(id, 60); !ok {
			t.Fatalf("Drain #%d: expected ok=true", i+1)
		}
	}
	// Next take should fail.
	if ok, _ := r.Take(id, 60); ok {
		t.Fatalf("Take after drain: expected rate-limited")
	}
	// Advance 2 seconds → 2 tokens accrue.
	advance()
	advance()
	for i := 0; i < 2; i++ {
		if ok, _ := r.Take(id, 60); !ok {
			t.Fatalf("Take after refill #%d: expected ok=true", i+1)
		}
	}
	// Third post-refill take should fail (only 2 accrued).
	if ok, _ := r.Take(id, 60); ok {
		t.Fatalf("Third post-refill take: expected rate-limited")
	}
}

// TestLimiter_Take_RefillCappedAtCapacity verifies that a long
// idle period doesn't accumulate tokens beyond the bucket cap.
func TestLimiter_Take_RefillCappedAtCapacity(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now, advance := fakeClock(start, time.Hour)
	r := peraccount.NewLimiter()
	r.SetClock(now)

	id := uuid.New()
	// Take one to register the bucket, then idle an hour.
	if ok, _ := r.Take(id, 60); !ok {
		t.Fatalf("Setup take: expected ok=true")
	}
	advance() // +1h
	// Bucket should have 60 tokens (capped), not 3600.
	successes := 0
	for i := 0; i < 100; i++ {
		if ok, _ := r.Take(id, 60); !ok {
			break
		}
		successes++
	}
	if successes != 60 {
		t.Errorf("Long-idle refill: got %d successes, want 60 (bucket cap)", successes)
	}
}

// TestLimiter_CacheLimits_TTL verifies that CachedLimits returns
// the entry within 60s and discards it after 60s.
func TestLimiter_CacheLimits_TTL(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now, advance := fakeClock(start, 30*time.Second)
	r := peraccount.NewLimiter()
	r.SetClock(now)

	id := uuid.New()
	limits := api.Limits{DebugTelemetryEnabled: true, DebugTelemetryRequestsPerMinute: 60}
	r.CacheLimits(id, limits)

	// Within 60s — cache hit.
	if _, ok := r.CachedLimits(id); !ok {
		t.Fatalf("CachedLimits within TTL: expected hit")
	}
	advance() // +30s — total elapsed = 30s, still within TTL
	if _, ok := r.CachedLimits(id); !ok {
		t.Fatalf("CachedLimits after 30s: expected hit")
	}
	advance() // +30s — total elapsed = 60s, at the boundary
	// The TTL check is `> 60s` strict, so 60s exactly is still a hit.
	if _, ok := r.CachedLimits(id); !ok {
		t.Fatalf("CachedLimits at 60s exact: expected hit (> strict)")
	}
	advance() // +30s — total elapsed = 90s, expired
	if _, ok := r.CachedLimits(id); ok {
		t.Fatalf("CachedLimits after 90s: expected miss")
	}
}

// TestLimiter_CacheLimits_PerAccount verifies that the cache is
// keyed per-account — entries for one customer don't leak to
// another.
func TestLimiter_CacheLimits_PerAccount(t *testing.T) {
	r := peraccount.NewLimiter()
	idA := uuid.New()
	idB := uuid.New()

	r.CacheLimits(idA, api.Limits{DebugTelemetryEnabled: true, DebugTelemetryRequestsPerMinute: 100})
	if _, ok := r.CachedLimits(idB); ok {
		t.Fatalf("CachedLimits(idB): expected miss (only idA cached)")
	}
	if _, ok := r.CachedLimits(idA); !ok {
		t.Fatalf("CachedLimits(idA): expected hit")
	}
}

// TestLimiter_Take_CapFrozenOnFirstCall (PR-D code-review #2):
// the bucket capacity is frozen on the first Take call for an
// account. A subsequent caller passing a different cap must NOT
// silently override — the refill rate stays anchored to the
// first caller's cap, so PR-B IncrementRequestTelemetry and
// PR-D WriteSpansSummary cannot oscillate the bucket by which
// one ran last.
func TestLimiter_Take_CapFrozenOnFirstCall(t *testing.T) {
	r := peraccount.NewLimiter()
	id := uuid.New()

	// First call: cap = 100 (PR-B's Hobby-style cap).
	taken, _ := r.Take(id, 100)
	if !taken {
		t.Fatalf("first Take(cap=100): expected ok")
	}

	// Drain the bucket.
	for i := 0; i < 99; i++ {
		if taken, _ := r.Take(id, 100); !taken {
			t.Fatalf("drain Take[%d]: expected ok", i)
		}
	}
	// Bucket is empty now (100 tokens, 100 consumed).

	// Second caller passes cap = 50000 (PR-D's Scale fallback).
	// The frozen cap=100 must apply — the bucket is still empty.
	taken, retryMs := r.Take(id, 50000)
	if taken {
		t.Fatalf("second Take(cap=50000): expected rate-limited (cap is frozen at 100, bucket empty)")
	}
	if retryMs < 1 {
		t.Errorf("retryMs = %d, want > 0", retryMs)
	}
	// retryMs is computed from b.cap=100, so 60000/100 = 600 ms.
	// A buggy implementation that used the second caller's cap
	// (50000) would return retryMs = 60000/50000 = 1 ms.
	if retryMs == 1 {
		t.Errorf("retryMs = 1, want ~600 (cap was frozen at 100, second caller's cap=50000 must NOT override)")
	}
}

// adr: 127
func TestLimiterResolvedPlanReconfiguresExistingBucket(t *testing.T) {
	for _, tc := range []struct {
		name      string
		old, next int
	}{
		{"upgrade", 60, 120},
		{"downgrade", 120, 60},
		{"disable", 60, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
			r := peraccount.NewLimiter()
			r.SetClock(func() time.Time { return now })
			id := uuid.New()
			for i := 0; i < tc.old; i++ {
				if ok, _ := r.Take(id, tc.old); !ok {
					t.Fatal("initial bucket exhausted early")
				}
			}
			r.CacheLimits(id, api.Limits{DebugTelemetryRequestsPerMinute: tc.next})
			now = now.Add(time.Minute)
			for i := 0; i < tc.next; i++ {
				// A stale in-flight caller must not undo an authoritative refresh.
				if ok, _ := r.Take(id, tc.old); !ok {
					t.Fatalf("resolved cap %d: token %d denied", tc.next, i)
				}
			}
			if ok, _ := r.Take(id, tc.old); ok {
				t.Fatalf("allowed more than resolved cap %d", tc.next)
			}
		})
	}
}

func TestLimiterCachedPlanWinsBeforeFirstTake(t *testing.T) {
	r := peraccount.NewLimiter()
	r.SetClock(func() time.Time { return time.Unix(0, 0) })
	id := uuid.New()
	r.CacheLimits(id, api.Limits{DebugTelemetryRequestsPerMinute: 1})
	if ok, _ := r.Take(id, 50000); !ok {
		t.Fatal("initial token denied")
	}
	if ok, _ := r.Take(id, 50000); ok {
		t.Fatal("fallback cap overrode resolved plan")
	}
}

func TestLimiterRetryDelayUsesFractionalBalanceAndRoundsUp(t *testing.T) {
	for _, tc := range []struct {
		cap     int
		elapsed time.Duration
		want    int64
	}{
		{60, 750 * time.Millisecond, 250},
		{7, 0, 8572},
	} {
		now := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
		r := peraccount.NewLimiter()
		r.SetClock(func() time.Time { return now })
		id := uuid.New()
		for i := 0; i < tc.cap; i++ {
			r.Take(id, tc.cap)
		}
		now = now.Add(tc.elapsed)
		if ok, delay := r.Take(id, tc.cap); ok || delay != tc.want {
			t.Fatalf("cap %d after %s: ok=%v retry=%d, want %d", tc.cap, tc.elapsed, ok, delay, tc.want)
		}
		now = now.Add(time.Duration(tc.want) * time.Millisecond)
		if ok, _ := r.Take(id, tc.cap); !ok {
			t.Fatal("retry hint woke caller before a token was available")
		}
	}
}

func TestLimiterRefreshDoesNotMintTokensOrRetroactivelyChangeRate(t *testing.T) {
	now := time.Unix(0, 0)
	r := peraccount.NewLimiter()
	r.SetClock(func() time.Time { return now })
	id := uuid.New()
	for i := 0; i < 60; i++ {
		r.Take(id, 60)
	}
	now = now.Add(time.Second) // One token at the old rate.
	r.CacheLimits(id, api.Limits{DebugTelemetryRequestsPerMinute: 120})
	if ok, _ := r.Take(id, 120); !ok {
		t.Fatal("old-rate accrued token lost")
	}
	r.CacheLimits(id, api.Limits{DebugTelemetryRequestsPerMinute: 120})
	if ok, _ := r.Take(id, 120); ok {
		t.Fatal("refresh minted a token")
	}
	now = now.Add(500 * time.Millisecond)
	if ok, _ := r.Take(id, 120); !ok {
		t.Fatal("new refill rate not applied")
	}
}
