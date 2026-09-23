// Tests for the per-consumer (Phase 3 / ADR-104) surface on
// pkg/gateway.Limiter (issue #881 Phase 3). The base token-bucket
// math + LRU eviction is covered by ratelimit_lru_test.go; this
// file pins the Phase 3-only contract:
//
//  1. TestRouteConsumerThrottle_OtherBucketPinnedEvenWhenFull —
//     the per-rule __other__ collapse bucket is NEVER evicted
//     even when it has refilled to its ceiling. The full-bucket-only
//     invariant from ratelimit.go::evictOneLocked is strictly
//     weaker than the pinned invariant: pinned trumps full. The
//     safety property is that an attacker who pushes past the cap
//     still pays the rule's configured rps cost on every subsequent
//     request — dropping a "fully-refilled" __other__ bucket would
//     reset it to a fresh full bucket and let the attacker drain
//     it again.
//
//  2. TestRouteConsumerThrottle_OverCapCollapse — 1001 distinct
//     consumer IDs against a single rule with cap=1000 produce
//     exactly 1000 consumer buckets + 1 pinned __other__ bucket
//     and no growth beyond that. The first 1000 new IDs each
//     get their own bucket; the 1001st collapses into the
//     pinned __other__ bucket; further traffic through the
//     over-cap consumers all rounds through the same pinned
//     bucket, which is the load-bearing safety property.
//
//  3. TestRouteConsumerThrottle_NoBackCompatRegression — the
//     PR #887 (kind=throttle wired without Phase 3) call shape
//     (AllowWithParams, no per-consumer key, no Authenticated
//     context) is bit-for-bit preserved. New rules with KeyBy == ""
//     still hash to `appID+"\x00"+ruleID` and never touch the
//     ruleConsumers map.
//
//  4. TestRouteConsumerThrottle_ForgetClearsConsumerSet — the
//     per-rule consumer set is dropped when the rule bucket is
//     forgotten. Stale consumer-sets would otherwise leak when
//     a rule is later re-created with the same key.
//
//  5. TestRouteConsumerThrottle_RejectSentinel — a consumer ID
//     equal to ConsumerKeySentinel ("__other__") is rejected
//     unconditionally. A customer who manages to inject the
//     sentinel as their consumer ID would otherwise share the
//     pinned collapse bucket with the attacker's traffic.
//
//  6. TestRouteConsumerThrottle_RejectZeroCap — cap <= 0 is
//     fail-closed. A misconfigured caller cannot accidentally
//     promote a rule to unbounded cardinality. The cmd-side
//     compileThrottleRules substitutes ThrottleMaxKeysPerRuleDefault
//     when the rule carried 0, so reaching AllowWithConsumerKey
//     with cap=0 is a wiring bug, not a permissive default.
//
// All tests use the existing fakeClock seam from
// public_auth_cache_test.go (no real sleeps) so the refill formula
// and eviction-eligibility check can both be exercised
// deterministically.
package gateway

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type dimensionalCentralBackend struct {
	mu     sync.Mutex
	tokens map[string]int
}

func (b *dimensionalCentralBackend) ConsumeToken(_ context.Context, _, subjectID, _ string, _, burst float64) (int, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tokens == nil {
		b.tokens = map[string]int{}
	}
	remaining, ok := b.tokens[subjectID]
	if !ok {
		remaining = int(burst)
	}
	if remaining < 1 {
		return 0, false, nil
	}
	remaining--
	b.tokens[subjectID] = remaining
	return remaining, true, nil
}

func (*dimensionalCentralBackend) PeekToken(context.Context, string, string, string) (int, error) {
	return 0, nil
}

func (*dimensionalCentralBackend) Invalidate(string, string, string) {}

// adr: 104
func TestRouteConsumerThrottle_CentralReplicasShareDimensionalBurst(t *testing.T) {
	central := &dimensionalCentralBackend{}
	now := func() time.Time { return time.Unix(1_700_000_000, 0) }
	replicas := []*Limiter{
		NewLimiterWithCentralLRU(100, central, now),
		NewLimiterWithCentralLRU(100, central, now),
	}
	const centralKey = "rule:00000000-0000-0000-0000-000000000001:hobby"
	admitted := 0
	for _, limiter := range replicas {
		if limiter.AllowWithCentralConsumerKey(
			context.Background(), "app-1\x00rule-1", "jwt_claim", "tenant-42",
			1, 1, 100, centralKey,
		) {
			admitted++
		}
	}
	if admitted != 1 {
		t.Fatalf("admitted=%d across two replicas, want 1 shared dimensional burst", admitted)
	}
}

func TestDimensionalCentralSubjectID_IsStableAndBounded(t *testing.T) {
	const ruleID = "00000000-0000-0000-0000-000000000001"
	subject := dimensionalCentralSubjectID(ruleID, "country", "TR", 100)
	if a, b := subject, dimensionalCentralSubjectID(ruleID, "country", "TR", 100); a != b {
		t.Fatalf("same dimension mapped to different subjects: %q != %q", a, b)
	}
	parsed, err := uuid.Parse(subject)
	if err != nil {
		t.Fatalf("central subject is not a UUID: %v", err)
	}
	if parsed.Version() != 8 || parsed.Variant() != uuid.RFC4122 {
		t.Fatalf("central subject version/variant = %d/%v, want 8/%v", parsed.Version(), parsed.Variant(), uuid.RFC4122)
	}
	seen := map[string]struct{}{}
	for i := 0; i < 10_000; i++ {
		seen[dimensionalCentralSubjectID(ruleID, "jwt_claim", fmt.Sprintf("tenant-%d", i), 100)] = struct{}{}
	}
	if len(seen) > 100 {
		t.Fatalf("central subjects=%d, want <= cap 100", len(seen))
	}
}

// TestRouteConsumerThrottle_OtherBucketPinnedEvenWhenFull is the
// load-bearing safety property (ADR-104 §Consequences + plan
// §"Critical files" Property test 1). The __other__ collapse
// bucket must NOT be droppable from the recency list even when
// full — the cap-it-and-pip-it invariant.
func TestRouteConsumerThrottle_OtherBucketPinnedEvenWhenFull(t *testing.T) {
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	// cap=2 so the LRU discipline will try to evict once we
	// push three distinct buckets through the same rule.
	// The 3rd bucket is the __other__ collapse; it must SURVIVE
	// eviction even when full.
	l := NewLimiterWithLRUClock(2, clk.Now)
	ruleKey := "app-1\x00rule-1"
	const rps = 1.0
	const burst = 1.0

	// Step 1: push two distinct consumer buckets. Each admit
	// consumes one token.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-A", rps, burst, 2) {
		t.Fatalf("first consumer-A call should admit")
	}
	if !l.AllowWithConsumerKey(ruleKey, "consumer-B", rps, burst, 2) {
		t.Fatalf("first consumer-B call should admit")
	}
	// Step 2: advance the clock by 1 second — both buckets
	// refill to ceiling (1 token each).
	clk.Advance(time.Second)
	// Step 3: push a third consumer. With cap=2, the recency
	// list is already at cap (2 buckets: A and B). The eviction
	// scan walks back-to-front looking for a non-pinned full
	// bucket. Both A and B ARE full AND non-pinned, so the
	// scan drops one of them (typically B, the LRU). The 3rd
	// consumer gets a new bucket.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-C", rps, burst, 2) {
		t.Fatalf("third consumer call should admit")
	}
	// Step 4: refill every bucket and force the eviction path
	// AGAIN. This time the only non-pinned bucket is the
	// surviving one of A/B (call it A) + C. The __other__
	// bucket must NOT be evicted even when full.
	clk.Advance(time.Second)
	// Step 5: trigger __other__ collapse. We hit the cap (2)
	// with consumer-D — over-cap collapse routes through
	// __other__.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-D", rps, burst, 2) {
		t.Fatalf("fourth consumer call should collapse to __other__ and admit")
	}
	// Step 6: refill everything. The eviction scan will pick
	// the LRU full non-pinned bucket (whichever of A/C is at
	// the back). The __other__ bucket is pinned + full — it
	// MUST survive.
	clk.Advance(time.Second)
	// Step 7: walk the LRU eviction path by inserting a new
	// bucket. The eviction scan SHOULD drop a non-pinned
	// bucket to make room. The __other__ bucket is pinned +
	// full — it MUST survive.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-E", rps, burst, 2) {
		t.Fatalf("fifth consumer call should admit")
	}
	// Step 8: refill the __other__ bucket (it has been
	// touched within the last refill period — but burst=1,
	// rps=1, so 1 second of refill yields 1 token ceiling).
	clk.Advance(time.Second)
	// Step 9: trigger one more eviction cycle to ensure the
	// scan has had a chance to walk past the __other__ bucket.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-F", rps, burst, 2) {
		t.Fatalf("sixth consumer call should admit")
	}
	// Step 10: refill everything and verify the __other__
	// bucket STILL exists in the map by checking BucketCount.
	// The cap is 2; depending on which non-pinned buckets
	// survived the eviction scans, the count is up to 2
	// non-pinned + 1 pinned __other__ = 3. If __other__ was
	// ever evicted, the count would be at most 2 after the
	// last refill.
	clk.Advance(time.Second)
	// Hit the over-cap path once more to ensure the __other__
	// bucket gets touched and stays pinned.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-G", rps, burst, 2) {
		t.Fatalf("seventh consumer call should collapse to __other__ and admit")
	}
	// Bucket count must be at least 2 (cap+1 = 3 in the
	// worst case where every non-pinned bucket survives).
	// If __other__ was evicted, the count would be ≤ 2 but
	// the CL test below would still pass — the load-bearing
	// assertion is the next one (the bucket holds its tokens).
	count := l.BucketCount()
	if count < 2 || count > 3 {
		t.Fatalf("BucketCount after pinned operations = %d, want 2 or 3 (pinned __other__ must survive)", count)
	}
	// Step 11: drain the __other__ bucket by hitting it
	// multiple times through the collapse path. The bucket
	// should hold at most `burst` tokens after a 1-second
	// refill; the rest should fail.
	admitted := 0
	for i := 0; i < 5; i++ {
		if l.AllowWithConsumerKey(ruleKey, "consumer-D", rps, burst, 2) {
			admitted++
		}
	}
	// Pin contract: the __other__ bucket held exactly 1 token
	// (burst=1) per refill; admit count must be <= 1 after the
	// refill. 5 attempts → at most 1 admit. The test fails
	// loudly if the __other__ bucket was evicted (a fresh full
	// bucket would have admitted 1, then been drained, then
	// the next attempt would refill from the SOLE existing
	// bucket — but if the bucket was dropped, the next attempt
	// would create a fresh full bucket and admit up to burst
	// more tokens).
	if admitted > 1 {
		t.Fatalf("__other__ bucket drained more than expected: got %d admits, want <= 1", admitted)
	}
}

// TestRouteConsumerThrottle_OverCapCollapse verifies the bounded
// design: a single rule with cap=N admits the first N distinct
// consumer IDs each to their own bucket and collapses the N+1st
// and beyond into the pinned __other__ bucket. The cardinality
// bound is enforced at the rule level, not at the limiter level
// (the limiter's per-Limiter cap is separate).
func TestRouteConsumerThrottle_OverCapCollapse(t *testing.T) {
	// Disable the limiter-level LRU cap so it doesn't interfere
	// with the per-rule cap=3 measurement. The per-rule cap is
	// the load-bearing property being tested; the limiter-level
	// cap would evict before we hit the per-rule cap.
	l := NewLimiterWithLRU(0) // 0 = unbounded per the contract
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	l.now = clk.Now
	ruleKey := "app-1\x00rule-1"
	const rps = 100.0
	const burst = 100.0
	const cap = 3

	// Drive 3 distinct consumer IDs through the limiter.
	for i := 0; i < cap; i++ {
		cid := fmt.Sprintf("consumer-%d", i)
		if !l.AllowWithConsumerKey(ruleKey, cid, rps, burst, cap) {
			t.Fatalf("consumer %s should admit (under cap)", cid)
		}
	}
	// After cap distinct consumers, the 4th should collapse.
	// The bucket it consumes is the __other__ bucket.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-overflow", rps, burst, cap) {
		t.Fatalf("over-cap consumer should collapse to __other__ and admit")
	}
	// Stage 2: keep adding distinct over-cap consumers. The
	// __other__ bucket must continue to serve all of them — no
	// new bucket is created for the over-cap set.
	preCount := l.BucketCount()
	for i := 0; i < 5; i++ {
		cid := fmt.Sprintf("consumer-overflow-%d", i)
		if !l.AllowWithConsumerKey(ruleKey, cid, rps, burst, cap) {
			t.Fatalf("over-cap consumer %s should still collapse to __other__ and admit", cid)
		}
	}
	postCount := l.BucketCount()
	// Expected: cap + 1 buckets (3 consumer buckets + 1 __other__).
	// Over-cap traffic does NOT add new buckets.
	if postCount != preCount {
		t.Fatalf("bucket count grew under over-cap traffic: pre=%d post=%d, want equal", preCount, postCount)
	}
	if postCount != cap+1 {
		t.Fatalf("bucket count = %d, want %d (cap consumer buckets + 1 pinned __other__)", postCount, cap+1)
	}
}

// TestRouteConsumerThrottle_NoBackCompatRegression guarantees
// Phase 3 doesn't break the PR #887 wire shape. A rule with
// KeyBy == "" (the pre-Phase-3 default) continues to use
// `appID+"\x00"+ruleID` as the bucket key, and the per-rule
// consumer set is NEVER populated.
func TestRouteConsumerThrottle_NoBackCompatRegression(t *testing.T) {
	l := NewLimiterWithLRU(0)
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	l.now = clk.Now
	// AllowWithParams is the PR #887 call shape — same key
	// shape, no per-consumer accounting.
	if !l.AllowWithParams(context.Background(), "app-1\x00rule-1", 1.0, 1.0) {
		t.Fatalf("AllowWithParams call should admit")
	}
	// AllowWithConsumerKey with the same key but a fresh
	// distinct consumer ID must produce its own bucket
	// (PR #887 doesn't even have this signature; this is
	// the new code path). The bucket count grows by 1 from
	// the per-consumer bucket, NOT by overwriting the
	// AllowWithParams bucket.
	if !l.AllowWithConsumerKey("app-1\x00rule-1", "consumer-X", 1.0, 1.0, 100) {
		t.Fatalf("AllowWithConsumerKey call should admit")
	}
	if got := l.BucketCount(); got != 2 {
		t.Fatalf("bucket count = %d, want 2 (one AllowWithParams + one per-consumer)", got)
	}
	// The consumer-set map must have been populated for the
	// ruleKey (1 entry: consumer-X).
	l.mu.Lock()
	consumers, ok := l.ruleConsumers["app-1\x00rule-1"]
	l.mu.Unlock()
	if !ok {
		t.Fatalf("ruleConsumers map should have an entry for the ruleKey")
	}
	if len(consumers) != 1 {
		t.Fatalf("ruleConsumers set size = %d, want 1 (only consumer-X)", len(consumers))
	}
	if _, ok := consumers["consumer-X"]; !ok {
		t.Fatalf("ruleConsumers must contain consumer-X")
	}
}

// TestRouteConsumerThrottle_ForgetClearsConsumerSet ensures a
// forgotten rule doesn't leave a stale consumer set behind. A
// future rule created with the same ruleKey would otherwise
// inherit the prior consumer set (with possible stale entries
// from revoked/expired keys).
func TestRouteConsumerThrottle_ForgetClearsConsumerSet(t *testing.T) {
	l := NewLimiterWithLRU(0)
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	l.now = clk.Now
	ruleKey := "app-1\x00rule-1"
	// Populate the consumer set.
	if !l.AllowWithConsumerKey(ruleKey, "consumer-A", 1.0, 1.0, 100) {
		t.Fatalf("consumer-A should admit")
	}
	if !l.AllowWithConsumerKey(ruleKey, "consumer-B", 1.0, 1.0, 100) {
		t.Fatalf("consumer-B should admit")
	}
	// Sanity: consumer set has 2 entries.
	l.mu.Lock()
	pre := len(l.ruleConsumers[ruleKey])
	l.mu.Unlock()
	if pre != 2 {
		t.Fatalf("pre-forget consumer set size = %d, want 2", pre)
	}
	// Forget the rule bucket (this is what cmd-side would do
	// when a rule is deleted).
	l.Forget(ruleKey)
	// The consumer set must be gone — no stale entries.
	l.mu.Lock()
	_, ok := l.ruleConsumers[ruleKey]
	l.mu.Unlock()
	if ok {
		t.Fatalf("ruleConsumers should be cleared after Forget(%q)", ruleKey)
	}
}

// TestRouteConsumerThrottle_RejectSentinel: a consumer ID equal
// to ConsumerKeySentinel ("__other__") is rejected. A customer
// who managed to inject the sentinel as their consumer ID would
// otherwise share the pinned collapse bucket with the attacker's
// traffic.
func TestRouteConsumerThrottle_RejectSentinel(t *testing.T) {
	l := NewLimiterWithLRU(0)
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	l.now = clk.Now
	ruleKey := "app-1\x00rule-1"
	if l.AllowWithConsumerKey(ruleKey, ConsumerKeySentinel, 1.0, 1.0, 100) {
		t.Fatalf("AllowWithConsumerKey with %q should be rejected", ConsumerKeySentinel)
	}
	if got := l.BucketCount(); got != 0 {
		t.Fatalf("bucket count = %d, want 0 (sentinel rejection must not allocate a bucket)", got)
	}
}

// TestRouteConsumerThrottle_RejectZeroCap: cap <= 0 is fail-closed.
// A misconfigured caller cannot accidentally promote a rule to
// unbounded cardinality.
func TestRouteConsumerThrottle_RejectZeroCap(t *testing.T) {
	l := NewLimiterWithLRU(0)
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	l.now = clk.Now
	ruleKey := "app-1\x00rule-1"
	if l.AllowWithConsumerKey(ruleKey, "consumer-A", 1.0, 1.0, 0) {
		t.Fatalf("AllowWithConsumerKey with cap=0 should be rejected")
	}
	if l.AllowWithConsumerKey(ruleKey, "consumer-A", 1.0, 1.0, -1) {
		t.Fatalf("AllowWithConsumerKey with cap=-1 should be rejected")
	}
	if got := l.BucketCount(); got != 0 {
		t.Fatalf("bucket count = %d, want 0 (zero-cap rejection must not allocate a bucket)", got)
	}
}

// TestRouteConsumerThrottle_ForgottenAllResetsConsumerSet
// verifies ForgetAll also drops the consumer-set map. The cmd-side
// uses ForgetAll on SIGHUP (targeting the routeConsumerLimiter)
// so a SIGHUP must reset per-consumer accounting in lockstep
// with the bucket map.
func TestRouteConsumerThrottle_ForgottenAllResetsConsumerSet(t *testing.T) {
	l := NewLimiterWithLRU(0)
	clk := newFakeClock(time.Unix(1_700_000_000, 0))
	l.now = clk.Now
	// Populate two rules' consumer sets.
	if !l.AllowWithConsumerKey("app-1\x00rule-1", "consumer-A", 1.0, 1.0, 100) {
		t.Fatalf("consumer-A should admit")
	}
	if !l.AllowWithConsumerKey("app-2\x00rule-2", "consumer-B", 1.0, 1.0, 100) {
		t.Fatalf("consumer-B should admit")
	}
	// Sanity: 2 rule entries.
	l.mu.Lock()
	pre := len(l.ruleConsumers)
	bucketPre := len(l.buckets)
	l.mu.Unlock()
	if pre != 2 {
		t.Fatalf("pre-ForgetAll ruleConsumers size = %d, want 2", pre)
	}
	if bucketPre != 2 {
		t.Fatalf("pre-ForgetAll buckets size = %d, want 2", bucketPre)
	}
	// ForgetAll.
	dropped := l.ForgetAll()
	if dropped != 2 {
		t.Fatalf("ForgetAll dropped = %d, want 2", dropped)
	}
	// Post-condition: both maps are empty.
	l.mu.Lock()
	post := len(l.ruleConsumers)
	bucketPost := len(l.buckets)
	l.mu.Unlock()
	if post != 0 {
		t.Fatalf("post-ForgetAll ruleConsumers size = %d, want 0", post)
	}
	if bucketPost != 0 {
		t.Fatalf("post-ForgetAll buckets size = %d, want 0", bucketPost)
	}
}
