// Package gateway holds gatewayd-internal's edge logic: per-app rate limiting, the host→
// app routing cache, and the wake gate that holds requests during a cold wake
// (spec §4.1). The HTTP/TLS server wires these together; each piece here is a
// self-contained, testable unit.
package gateway

import (
	"container/list"
	"context"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// LimiterEvictScan bounds how many least-recently-used buckets one
// eviction pass inspects before giving up. The scan walks back-to-front
// looking for a bucket that is safe to drop (see evictOneLocked); the
// bound keeps allowToken's worst case constant under the limiter mutex
// rather than O(n) when every bucket is mid-drain.
const LimiterEvictScan = 32

// centralConsultTimeout bounds the authoritative central-mode token consume
// before falling back to the local bucket during a Postgres outage. Central
// mode performs this operation for every request so replicas share one burst.
const centralConsultTimeout = 250 * time.Millisecond

// Limiter is a per-app token-bucket rate limiter (spec §4.1). Each app refills at
// its plan's rps with a plan burst; an over-limit request is rejected (the caller
// returns 429). The clock is injectable so the refill math is tested precisely.
//
// The same Limiter type is reused for per-account throttling (ADR-040 / issue
// #292) via AllowAccount — bucket key is the actor ID (app or account), and the
// rps/burst parameters come from a different accessor on Plan. Two *Limiter
// instances are constructed in pkg/gateway/handler.go (one per scope) so SIGHUP
// ForgetAll can target each scope independently and load tests can bypass one
// without bypassing the other.
//
// # Bucket-map growth and optional LRU discipline
//
// A limiter built with NewLimiter / NewLimiterWithClock keeps every bucket it
// has ever created: the map shrinks only via Forget / ForgetAccount /
// ForgetAll (SIGHUP). That is sound for the per-app and per-account scopes
// because the key space is bounded by the number of apps/accounts the node
// routes for.
//
// Scopes whose key space is NOT bounded that way (issue #881's per-route
// throttle keys on appID+ruleID) must use NewLimiterWithLRU, which caps the
// map and evicts least-recently-used buckets. Eviction is only ever applied
// to buckets that carry no state — see evictOneLocked for the invariant and
// why violating it would be a limit bypass.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time
	// noop, when true, makes Allow always return true. Set via WithNoop;
	// intended only for load tests that need to bypass the plan rps/burst
	// to assert the underlying handler path. DO NOT expose as a config
	// knob in production.
	noop bool
	// cap is the maximum number of buckets retained when LRU discipline is
	// enabled. Zero means unbounded (the historical behaviour that Allow /
	// AllowAccount rely on); a positive value activates the recency list.
	cap int
	// ll orders buckets most-recently-used at the front. Non-nil only when
	// cap > 0, so the unbounded path pays no list bookkeeping at all.
	ll *list.List
	// elems maps bucket key → its element in ll. Kept in lockstep with
	// buckets whenever ll is non-nil.
	elems map[string]*list.Element
	// ruleConsumers tracks the per-rule consumer set for Phase 3
	// (ADR-104, issue #881 Phase 3). Keyed by rule key
	// (appID+"\x00"+ruleID); values are the set of consumer IDs the
	// limiter has allocated a bucket for. Used by
	// AllowWithConsumerKey to decide whether a new consumer ID
	// should get its own bucket (set has room) or collapse into the
	// rule's __other__ bucket (set at cap). Lazy-initialised on
	// first contact; only populated by AllowWithConsumerKey, so the
	// Allow / AllowAccount / AllowWithParams paths pay no bookkeeping
	// cost. The maps are dropped in forgetLocked / ForgetAll when
	// the corresponding ruleKey bucket is forgotten.
	ruleConsumers map[string]map[string]struct{}
	// central is the cross-replica counter seam (ADR-104
	// amendment 5, issue #881 Phase 4). Defaults to
	// noopCentralBackend{} — every existing constructor sets it,
	// so behaviour is unchanged for callers that don't thread the
	// new NewLimiterWithCentral constructor. Central mode consumes from the
	// shared counter on every request; local state is a degraded fallback and
	// supplies response-header state.
	central CentralBackend
}

type bucket struct {
	tokens float64
	rps    float64
	burst  float64
	last   time.Time
	// key is duplicated here so evictOneLocked can delete the map entry
	// from a list element without a reverse scan.
	key string
	// pinned marks buckets that MUST NOT be evicted even when they refill
	// to ceiling. Phase 3 (ADR-104) sets pinned=true on the per-rule
	// __other__ collapse bucket so an attacker cannot weaponise the LRU
	// discipline to bypass the throttle by minting many distinct
	// consumer IDs and pushing the per-rule consumer-set past its
	// cap — the __other__ bucket is the only bucket that stays live
	// when the cap is exceeded, and dropping it would reset the
	// collapse to a full bucket, letting the attacker drain it again.
	pinned bool
}

// ConsumerKeySentinel is the suffix used for the per-rule __other__
// collapse bucket (ADR-104, issue #881 Phase 3). It is reserved —
// the limiter constructor rejects consumer IDs that exactly match
// this sentinel so a customer cannot smuggle it through key_by
// inputs and bypass the collapse.
const ConsumerKeySentinel = "__other__"

// AllowWithConsumerKey is the per-consumer variant of AllowWithParams
// (ADR-091 D20.5 amendment 4, ADR-104, issue #881 Phase 3). Caller
// (cmd-side applyEdgeRuleThrottle) supplies the rule's identity
// (ruleKey — the convention `appID + "\x00" + ruleID`), the
// consumer identity (consumerID — the API key id, JWT subject, or
// JWT custom-claim value, sourced from the Authenticated struct on
// the request context), the rule's per-rule rps and burst, and the
// per-rule consumer-set cap (MaxKeysPerRule).
//
// Bounded design (the load-bearing safety property — see ADR-104
// §Consequences): the limiter tracks the per-rule consumer set
// (`ruleConsumers` map) and when a NEW consumer ID arrives for a
// rule whose set is already at the cap, the limiter collapses that
// consumer into the rule's __other__ bucket instead of allocating a
// new consumer bucket. The __other__ bucket is pinned non-evictable
// (bucket.pinned = true) so even when full it cannot be dropped
// from the recency list — an attacker who pushed past the cap still
// pays the parent rule's rps cost on every subsequent request,
// because every over-cap consumer routes through the same pinned
// bucket.
//
// Back-compat: callers that don't care about per-consumer keying
// (KeyBy == "" or KeyBy == "none") should keep using AllowWithParams
// — it produces the bit-identical bucket key
// `appID + "\x00" + ruleID` and never touches the consumer-set map.
// Mixing the two for the same rule across requests is safe; the
// consumer-set map is only populated by AllowWithConsumerKey.
//
// rps, burst must be > 0 (cmd-side compileThrottleRules clamps to
// {1, 1} as defence-in-depth against direct-DB writes that bypassed
// apid-Validate). cap must be > 0; the validator rejects rules
// whose plan doesn't expose per-consumer throttling, so reaching
// this method with cap <= 0 is a wiring bug — fail closed.
func (l *Limiter) AllowWithConsumerKey(ruleKey, consumerID string, rps, burst float64, cap int) bool {
	if l.noop {
		return true
	}
	if rps < 0 || burst < 0 {
		return false
	}
	if cap <= 0 {
		// Fail closed: a missing cap is a wiring bug, not a
		// permissive default. A misconfigured caller cannot
		// accidentally promote a rule to unbounded cardinality.
		return false
	}
	if consumerID == ConsumerKeySentinel {
		// Reserved sentinel — a customer who managed to inject
		// "__other__" as a consumer ID is trying to share the
		// pinned collapse bucket with legitimate traffic. Refuse.
		return false
	}
	return l.allowWithConsumerKey(ruleKey, consumerID, rps, burst, cap)
}

// AllowWithCentralParams is the central-aware sibling of
// AllowWithParams (ADR-104 amendment 5, issue #881 Phase 4).
// When centralKey is non-empty, the shared counter is authoritative for every
// request. Empty centralKey reproduces AllowWithParams' local behavior.
//
// centralKey is the colon-separated triple
// "<scope>:<subject_id>:<plan>"; splitCentralKey parses it.
func (l *Limiter) AllowWithCentralParams(ctx context.Context, id string, rps, burst float64, centralKey string) bool {
	if l.noop {
		return true
	}
	if rps < 0 || burst < 0 {
		return false
	}
	return l.allowTokenWithCentralKey(ctx, id, rps, burst, centralKey)
}

// allowWithConsumerKey is the locked core of AllowWithConsumerKey.
// It separates the public API (input validation) from the inner
// locked method (which holds l.mu through the bucket lookup +
// consumer-set bookkeeping).
func (l *Limiter) allowWithConsumerKey(ruleKey, consumerID string, rps, burst float64, cap int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	otherKey := ruleKey + "\x00" + ConsumerKeySentinel
	consumerKey := ruleKey + "\x00" + consumerID

	// Look up the rule's consumer set. Lazy-init on first contact.
	consumers, ok := l.ruleConsumers[ruleKey]
	if !ok {
		consumers = map[string]struct{}{}
		l.ruleConsumers[ruleKey] = consumers
	}

	_, alreadyTracked := consumers[consumerID]
	bucketKey := consumerKey
	if !alreadyTracked && len(consumers) >= cap {
		// Over-cap collapse: every new consumer routes through
		// the __other__ bucket. The collapse bucket is pinned
		// non-evictable — see bucket.pinned doc.
		bucketKey = otherKey
	} else if !alreadyTracked {
		// First time we've seen this consumer for this rule;
		// add it to the set so subsequent requests bucket
		// per-consumer (not into __other__).
		consumers[consumerID] = struct{}{}
	}

	return l.allowTokenKeyedLocked(bucketKey, rps, burst, otherKey, now)
}

// ConsumerIsTracked reports whether consumerID has its own bucket
// under ruleKey (i.e. is in the per-rule consumer set, NOT
// collapsed into the __other__ bucket). Phase 4 H1's applier
// (handler.go::applyEdgeRuleThrottle) uses this to decide whether
// to emit X-RouteRateLimit-Policy=per-consumer on the 429 path:
// the value is set when the per-consumer rule's consumer has
// collapsed to __other__ (i.e. NOT tracked). False is the
// back-compat answer for rules where KeyBy ∈ {"", "none"} — the
// rule-only bucket key path never reaches here.
//
// Lock-safe (mu is a sync.Mutex today; locking is cheap — the map
// is small and the lookup is a constant-time hash read); safe to
// call from the hot path. noop limiters return true (defensive —
// applier still emits the header; the noop rule-level
// AllowWithParams also returns true).
func (l *Limiter) ConsumerIsTracked(ruleKey, consumerID string) bool {
	if l == nil || l.noop {
		return true
	}
	if consumerID == "" || consumerID == ConsumerKeySentinel {
		// Reserved sentinel is never tracked — appliers should
		// never pass it, and the limiter's AllowWithConsumerKey
		// rejects it. Treating "not tracked" as the answer is
		// the conservative policy-header reading.
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	consumers, ok := l.ruleConsumers[ruleKey]
	if !ok {
		return false
	}
	_, tracked := consumers[consumerID]
	return tracked
}

// allowTokenKeyedLocked is the shared refill math used by
// allowToken + allowWithConsumerKey. The pinnedKey parameter is the
// rule's __other__ bucket key when this is a per-consumer call; the
// empty string means "no pin" (the Allow / AllowAccount /
// AllowWithParams path). A bucket whose key equals pinnedKey is
// pinned non-evictable on creation and stay-pinned across
// subsequent visits — so the __other__ collapse bucket survives
// forever even when full, which is the load-bearing safety
// property of the bounded design.
func (l *Limiter) allowTokenKeyedLocked(id string, rps, burst float64, pinnedKey string, now time.Time) bool {
	b := l.buckets[id]
	if b == nil {
		if l.cap > 0 && len(l.buckets) >= l.cap {
			l.evictOneLocked(now)
		}
		b = &bucket{tokens: burst, rps: rps, burst: burst, last: now, key: id}
		if pinnedKey != "" && id == pinnedKey {
			b.pinned = true
		}
		l.buckets[id] = b
		if l.ll != nil {
			l.elems[id] = l.ll.PushFront(b)
		}
	} else {
		b.rps, b.burst = rps, burst
		b.tokens += now.Sub(b.last).Seconds() * b.rps
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
		if l.ll != nil {
			if el := l.elems[id]; el != nil {
				l.ll.MoveToFront(el)
			}
		}
	}

	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// NewLimiter returns a limiter using the wall clock.
func NewLimiter() *Limiter {
	return &Limiter{
		buckets:       map[string]*bucket{},
		now:           time.Now,
		ruleConsumers: map[string]map[string]struct{}{},
		central:       noopCentralBackend{},
	}
}

// NewLimiterWithClock returns a limiter whose internal clock is the supplied
// func. Test seam for the cmd/gatewayd-internal/backend_test.go 1001-request
// acceptance (issue #292 / ADR-040) — frozen-clock tests cannot depend on
// the package-private now field. Production code uses NewLimiter.
func NewLimiterWithClock(now func() time.Time) *Limiter {
	return &Limiter{
		buckets:       map[string]*bucket{},
		now:           now,
		ruleConsumers: map[string]map[string]struct{}{},
		central:       noopCentralBackend{},
	}
}

// NewLimiterWithCentral returns a Limiter that consults a CentralBackend
// on every request (ADR-104 amendment 5, issue #881 Phase 4). Pass nil to
// reproduce the noopCentralBackend default —
// callers that don't yet thread the [ratelimit] mode TOML knob
// (cmd/gatewayd-internal/config.go) keep today's byte-for-byte
// behaviour. Production wiring lives in cmd/gatewayd-internal/run.go.
//
// The in-process decision remains available only as a bounded degraded-mode
// fallback if the central store cannot be reached.
func NewLimiterWithCentral(central CentralBackend) *Limiter {
	l := NewLimiter()
	if central != nil {
		l.central = central
	}
	return l
}

// NewLimiterWithCentralAndClock is NewLimiterWithCentral with an
// injectable clock. Frozen-clock tests that exercise central-mode
// paths use this constructor (mirrors NewLimiterWithClock).
func NewLimiterWithCentralAndClock(central CentralBackend, now func() time.Time) *Limiter {
	l := NewLimiterWithClock(now)
	if central != nil {
		l.central = central
	}
	return l
}

// NewLimiterWithLRU returns a limiter that retains at most cap buckets,
// evicting least-recently-used entries once the map is full. Intended for
// scopes whose key space is not bounded by the app/account count — issue
// #881's per-route throttle keys on (appID, ruleID), so a customer with many
// rules across many apps would otherwise grow the map without limit.
//
// cap <= 0 yields the historical unbounded limiter, so callers can pass a
// config value through without a nil check. Pass EdgeRuleCacheCap to match
// the edge-rule cache's ceiling.
//
// Eviction never resets an in-flight throttle: only buckets that have
// refilled completely are candidates (see evictOneLocked).
func NewLimiterWithLRU(cap int) *Limiter {
	return newLimiterWithLRU(cap, time.Now)
}

// NewLimiterWithLRUClock is NewLimiterWithLRU with an injectable clock. Test
// seam mirroring NewLimiterWithClock; eviction behaviour depends on elapsed
// time, so the tests need a frozen clock to be deterministic.
func NewLimiterWithLRUClock(cap int, now func() time.Time) *Limiter {
	return newLimiterWithLRU(cap, now)
}

func newLimiterWithLRU(cap int, now func() time.Time) *Limiter {
	l := &Limiter{
		buckets:       map[string]*bucket{},
		now:           now,
		ruleConsumers: map[string]map[string]struct{}{},
		central:       noopCentralBackend{},
	}
	if cap > 0 {
		l.cap = cap
		l.ll = list.New()
		l.elems = map[string]*list.Element{}
	}
	return l
}

// NewLimiterWithCentralLRU combines the LRU discipline (Phase 2 cap
// on the bucket map) with the central-mode backend (Phase 4 cross-
// replica serialisation). Used by the per-consumer LRU limiter that
// the rule-scope throttle carries (pkg/gateway/handler.go). The
// central field can be nil to fall back to the noop backend.
func NewLimiterWithCentralLRU(cap int, central CentralBackend, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	l := newLimiterWithLRU(cap, now)
	if central != nil {
		l.central = central
	}
	return l
}

// Allow reports whether a request for appID on plan may proceed, consuming a
// token if so. Plan rps/burst come from the limits table (never inlined).
//
// ctx is propagated so the central-mode consume can be bounded by both the
// caller deadline and centralConsultTimeout. Local mode ignores ctx.
func (l *Limiter) Allow(ctx context.Context, appID string, plan api.Plan) bool {
	if l.noop {
		return true
	}
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return false
	}
	return l.allowTokenWithCentralKey(ctx, appID, float64(limits.RateLimitRPS), float64(limits.RateLimitBurst),
		"app:"+appID+":"+string(plan))
}

// AllowAccount consumes one token from the bucket keyed by accountID (ADR-040 /
// issue #292). Bucket parameters come from Plan.RateLimitPerAccountRPM —
// internal math divides by 60 to derive rps and uses the RPM value as the
// burst ceiling, so an account can absorb a burst of `RPM` requests before
// the per-minute refill kicks in. Distinct from Allow (per-app) so a botnet
// rotating across many apps within an account cannot evade this limiter by
// keeping per-app rps low.
// AllowWithParams is the rule-scoped variant for kind=throttle
// (ADR-091 D20.5 amendment, issue #881). Caller (cmd-side
// applyEdgeRuleThrottle) supplies the rule's bucket key (the
// convention `appID + "\x00" + ruleID` so the bucket is bounded by
// configured rules, not traffic — see plan §"Bucket key") together
// with the rule's per-rule rps and burst. Returns true on a
// consumed token, false when the bucket is empty (caller responds
// 429 with `x-faas-rate-limit-scope: route` +
// `X-RouteRateLimit-{Limit,Remaining,Reset}` headers — see
// applyEdgeRuleThrottle). Distinct from Allow (per-app) and
// AllowAccount (per-account) so customer-configured throttles
// don't fight the platform-shared limits for map slots.
//
// rps must be > 0; the cmd-side compile clamps to 1 as
// defence-in-depth against a direct-DB row that bypassed
// apid-Validate, so call sites can rely on rps/burst > 0 here.
// When the limiter was built with NewLimiterWithLRU, each call
// also marks the bucket most-recently-used and, on inserting
// into a full map, tries to evict (PR #887 invariant: only full
// buckets are evictable, so an attacker cannot force eviction to
// bypass a partially-drained rule bucket).
func (l *Limiter) AllowWithParams(ctx context.Context, id string, rps, burst float64) bool {
	if l.noop {
		return true
	}
	if rps < 0 || burst < 0 {
		return false
	}
	return l.allowToken(ctx, id, rps, burst)
}

// AllowAccount is the per-account sibling of Allow. Its context bounds the
// central-mode consume.
func (l *Limiter) AllowAccount(ctx context.Context, accountID string, plan api.Plan) bool {
	if l.noop {
		return true
	}
	rpm := plan.RateLimitPerAccountRPM()
	if rpm <= 0 {
		return false // fail closed on unknown plan (mirrors CronLimitPerAccount)
	}
	return l.allowTokenWithCentralKey(ctx, accountID, float64(rpm)/60.0, float64(rpm),
		"account:"+accountID+":"+string(plan))
}

// allowToken is the shared token-bucket math used by both Allow (per-app) and
// AllowAccount (per-account). Keyed by the actor ID; rps is the refill rate,
// burst is the bucket ceiling. Returns true on a consumed token, false when
// the bucket is empty (caller responds 429). Plan changes update rps/burst
// without losing in-flight tokens — see TestLimiterAllowAccount_PlanChange.
//
// When the limiter was built with NewLimiterWithLRU, each call also marks the
// bucket most-recently-used and, on inserting into a full map, tries to evict.
// The refill math itself is identical in both modes.
//
// # Central mode (ADR-104 amendment 5, issue #881 Phase 4)
//
// When the limiter was built with NewLimiterWithCentral, the
// noop default is replaced with the production CentralBackend.
// Every request calls central.ConsumeToken so the shared counter, rather than
// one bucket per process, is authoritative across gateway replicas.
//
// scope / subjectID / plan are caller-supplied via
// allowTokenWithCentralKey when the call site knows them
// (cmd/gatewayd-internal/run.go wires the per-scope mapping).
// The noop backend bypasses the central call, so the default behavior remains
// identical to the pre-Phase-4 in-process map.
func (l *Limiter) allowToken(ctx context.Context, id string, rps, burst float64) bool {
	return l.allowTokenWithCentralKey(ctx, id, rps, burst, "")
}

// allowTokenWithCentralKey is the central-aware variant of
// allowToken. When centralKey is empty, the local bucket is the only source
// of truth. When it is set, the central counter is authoritative and the local
// result is used only if the central backend returns an error.
func (l *Limiter) allowTokenWithCentralKey(ctx context.Context, id string, rps, burst float64, centralKey string) bool {
	l.mu.Lock()
	now := l.now()
	b := l.buckets[id]
	if b == nil {
		// Make room before inserting so the map never exceeds cap. If
		// nothing is safe to evict the map is allowed to overshoot —
		// correctness of in-flight throttles outranks the memory bound,
		// and BucketCount surfaces the overshoot to /metrics.
		if l.cap > 0 && len(l.buckets) >= l.cap {
			l.evictOneLocked(now)
		}
		b = &bucket{tokens: burst, rps: rps, burst: burst, last: now, key: id}
		l.buckets[id] = b
		if l.ll != nil {
			l.elems[id] = l.ll.PushFront(b)
		}
	} else {
		// A plan change updates the bucket's parameters without losing tokens.
		b.rps, b.burst = rps, burst
		b.tokens += now.Sub(b.last).Seconds() * b.rps
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
		if l.ll != nil {
			if el := l.elems[id]; el != nil {
				l.ll.MoveToFront(el)
			}
		}
	}

	localAllowed := false
	if b.tokens >= 1 {
		b.tokens--
		localAllowed = true
	}
	l.mu.Unlock()

	// Central mode makes the shared counter authoritative for every request.
	// The local decision is retained only as a bounded fallback if Postgres is
	// temporarily unavailable. This prevents one full burst per gateway replica.
	if centralKey == "" || l.isNoopBackend() {
		return localAllowed
	}
	scope, subjectID, plan, ok := splitCentralKey(centralKey)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, centralConsultTimeout)
	defer cancel()
	remaining, admitted, err := l.central.ConsumeToken(ctx, scope, subjectID, plan, rps, burst)
	if err != nil {
		return localAllowed
	}
	// Keep response headers and degraded fallback aligned with the latest
	// authoritative balance. Remaining==0 is a valid final-token admit.
	l.mu.Lock()
	if current := l.buckets[id]; current != nil {
		current.tokens = float64(remaining)
		current.last = l.now()
	}
	l.mu.Unlock()
	return admitted
}

// isNoopBackend reports whether the central field is the default
// noop. Type-asserting against noopCentralBackend avoids the
// interface-call overhead in the hot path when central mode is
// off (the default production posture). Type assertion on a
// concrete struct is a single cmp + branch.
func (l *Limiter) isNoopBackend() bool {
	_, ok := l.central.(noopCentralBackend)
	return ok
}

// evictOneLocked drops at most one bucket to make room. Caller holds l.mu.
//
// # The safety invariant
//
// A bucket may only be evicted when it has refilled to its ceiling
// (tokens >= burst). Such a bucket carries no information: re-creating it on
// the next request produces a full bucket, which is exactly what was dropped,
// so the eviction is unobservable.
//
// Evicting a partially drained bucket would be a LIMIT BYPASS. A caller who
// has spent its tokens is meant to receive 429s until the bucket refills;
// dropping that bucket hands them a fresh full one. Because bucket creation
// is driven by request traffic, an attacker who can push the map to its cap
// could then reset their own throttle at will — turning the memory bound into
// an attack primitive. The scan therefore skips any bucket that is mid-drain,
// even though that means the map can exceed cap under sustained pressure.
//
// The walk is back-to-front (least- to most-recently-used) and inspects at
// most LimiterEvictScan entries so the mutex is never held for an O(n) sweep.
func (l *Limiter) evictOneLocked(now time.Time) {
	if l.ll == nil {
		return
	}
	el := l.ll.Back()
	for i := 0; el != nil && i < LimiterEvictScan; i++ {
		prev := el.Prev()
		b, _ := el.Value.(*bucket)
		// Phase 3 (ADR-104): pinned buckets are NEVER evictable —
		// they back the per-rule __other__ collapse and dropping
		// them is a throttle bypass (a fresh full bucket is the
		// attacker's full allowance). The full-bucket-only check
		// remains for non-pinned buckets. Even when pinned AND
		// full, the scan skips — pinned trumps full.
		if b != nil && !b.pinned && bucketFull(b, now) {
			l.removeElementLocked(el)
			return
		}
		el = prev
	}
}

// bucketFull reports whether b would be at its ceiling if refilled to now.
// Mirrors allowToken's refill formula but without writing back, so a bucket
// is judged by what the next request would see rather than by the stale
// token count left over from its previous call.
func bucketFull(b *bucket, now time.Time) bool {
	if b.burst <= 0 {
		return false
	}
	tokens := b.tokens
	if dt := now.Sub(b.last).Seconds(); dt > 0 {
		tokens += dt * b.rps
	}
	return tokens >= b.burst
}

// removeElementLocked drops one element from both the recency list and the
// bucket map. Caller holds l.mu. Mirrors EdgeRuleCache.removeElement.
func (l *Limiter) removeElementLocked(el *list.Element) {
	b, _ := el.Value.(*bucket)
	l.ll.Remove(el)
	if b != nil {
		delete(l.buckets, b.key)
		delete(l.elems, b.key)
	}
}

// forgetLocked drops one key from the map and, when LRU discipline is on,
// from the recency list too. Caller holds l.mu.
//
// Phase 3 (ADR-104): when the forgotten key matches a ruleKey (i.e.
// begins with `appID+"\x00"+ruleID`), the per-rule consumer-set
// entry is also dropped. The ruleKey forms the prefix of every
// per-consumer bucket key (`ruleKey+"\x00"+consumerID`) so a stale
// consumer-set entry would otherwise leave orphan tracking when the
// rule itself is forgotten (e.g. via SIGHUP for that rule — though
// today SIGHUP targets ForgetAll which also clears the map; this
// branch handles the per-rule Forget path future-SIGHUP additions
// might add). No-op when the key is not a known ruleKey (e.g. a
// plain appID — Allow / AllowAccount paths).
func (l *Limiter) forgetLocked(id string) {
	delete(l.buckets, id)
	if l.ll != nil {
		if el := l.elems[id]; el != nil {
			l.ll.Remove(el)
			delete(l.elems, id)
		}
	}
	delete(l.ruleConsumers, id)
}

// Forget drops an app's bucket (e.g. on delete) to bound memory.
func (l *Limiter) Forget(appID string) {
	l.mu.Lock()
	l.forgetLocked(appID)
	l.mu.Unlock()
}

// Peek is a non-mutating snapshot of the token bucket for appID on plan.
// Used by the response-header writer (X-RateLimit-*) and by the
// /v1/internal/quota endpoint that backs the dashboard's bucket
// indicator (issue #314 / Finding 6). The function copies (tokens,
// last) into locals, applies the standard refill formula against the
// supplied clock WITHOUT writing back, and returns the same shape
// allowToken would have produced — but never decrements.
//
// Returns:
//
//	limit         = plan.RateLimitBurst (the bucket ceiling)
//	remaining     = floor(tokens) (token-count visible to the next caller)
//	resetSeconds  = ceil((1 - fractionalTokens) / rps) when tokens < 1,
//	               else 0 (the bucket is full and resets immediately on Allow)
//	ok            = true iff the bucket exists AND the plan is known;
//	               false on unknown plan, on missing bucket (app has
//	               never been Allow'd into the limiter — distinct from
//	               "0 remaining"), and on noop mode (the noop limiter
//	               has no bucket state — callers must skip the
//	               header write rather than emit zero-valued headers
//	               that imply exhaustion).
//
// Peek holds the limiter mutex for the same duration allowToken does
// — a copy + math. Concurrent Peek/Allow under -race is safe (the
// mutex is the sync point). The refill math intentionally mirrors
// allowToken so a snapshot read on tick N agrees with the consumption
// Allow on tick N+1 would have made.
//
// Cross-process limitation (documented in the header contract): in a
// multi-gatewayd-internal fleet each gateway process keeps its own bucket; the
// values reflect THIS gatewayd-internal's view. ADR-040 already flagged this;
// this surface makes the limitation visible, not gone.
func (l *Limiter) Peek(appID string, plan api.Plan) (limit, remaining, resetSeconds int, ok bool) {
	if l == nil || l.noop {
		return 0, 0, 0, false
	}
	limits, found := api.LimitsFor(plan)
	if !found {
		return 0, 0, 0, false
	}
	limit = limits.RateLimitBurst

	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[appID]
	if b == nil {
		// The bucket has never been Allow'd — the app exists in the
		// routing cache but hasn't been seen at the rate-limit edge
		// yet. Returning ok=false here distinguishes "never seen"
		// (dashboard renders "—") from "exhausted" (dashboard
		// renders "0 of N remaining").
		return limit, 0, 0, false
	}
	// Read the cached rps/burst the limiter stored when the bucket was
	// last Allow'd. allowToken refreshes these on every call (so a
	// mid-flight plan change is reflected); Peek picks up whatever
	// value is currently cached. Peek does NOT write back — leaving the
	// bucket unchanged is the non-mutating contract.
	rps, burst := b.rps, b.burst
	tokens := b.tokens
	last := b.last
	now := l.now()
	dt := now.Sub(last).Seconds()
	if dt > 0 {
		tokens += dt * rps
		if tokens > burst {
			tokens = burst
		}
	}
	if tokens < 0 {
		tokens = 0
	}
	remaining = int(tokens)
	if remaining < 0 {
		remaining = 0
	}
	if tokens < 1 {
		// ceil((1 - tokens) / rps) → seconds until the next whole
		// token becomes available. Adding 1 then floor() gives the
		// standard ceil semantics for positive denominators
		// (rps > 0 by construction in api.LimitsFor).
		resetSeconds = int((1-tokens)/rps + 1)
		if resetSeconds < 1 {
			resetSeconds = 1
		}
	}
	return limit, remaining, resetSeconds, true
}

// PeekAccount mirrors Peek for the per-account bucket (ADR-040). The
// rate units in the limits table for the per-account path are RPM,
// so allowToken divides by 60; Peek does the same so the visible
// reset time uses the same denominator the limiter actually refills
// at. Returns ok=false on noop, unknown plan, or missing bucket (the
// same "never seen" contract as Peek).
func (l *Limiter) PeekAccount(accountID string, plan api.Plan) (limit, remaining, resetSeconds int, ok bool) {
	if l == nil || l.noop {
		return 0, 0, 0, false
	}
	rpm := plan.RateLimitPerAccountRPM()
	if rpm <= 0 {
		return 0, 0, 0, false
	}
	rps := float64(rpm) / 60.0
	burst := float64(rpm)
	limit = rpm

	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[accountID]
	if b == nil {
		return limit, 0, 0, false
	}
	tokens := b.tokens
	last := b.last
	now := l.now()
	dt := now.Sub(last).Seconds()
	if dt > 0 {
		tokens += dt * rps
		if tokens > burst {
			tokens = burst
		}
	}
	if tokens < 0 {
		tokens = 0
	}
	remaining = int(tokens)
	if remaining < 0 {
		remaining = 0
	}
	if tokens < 1 {
		resetSeconds = int((1-tokens)/rps + 1)
		if resetSeconds < 1 {
			resetSeconds = 1
		}
	}
	return limit, remaining, resetSeconds, true
}

// PeekWithParams is the rule-scoped variant of Peek for
// kind=throttle (ADR-091 D20.5 amendment, issue #881). Returns the
// post-consume snapshot of the bucket at id (the same id used in
// AllowWithParams — the convention is `appID + "\x00" + ruleID`)
// using the supplied rps/burst as the bucket parameters. Caller
// (applyEdgeRuleThrottle → writeRouteRateLimitHeaders) renders
// the values into X-RouteRateLimit-{Limit,Remaining,Reset}. The
// limit field is the burst (i.e. the bucket ceiling), matching
// the X-RateLimit-Limit convention for the per-app + per-account
// paths so a customer dashboard that auto-parses limit-style
// headers sees one shape across all three scopes. Returns
// ok=false on noop or a missing bucket (the "never seen" contract
// shared with Peek / PeekAccount — the absence of the headers is
// the loader-side signal that the value is "not yet established").
func (l *Limiter) PeekWithParams(id string, rps, burst float64) (limit, remaining, resetSeconds int, ok bool) {
	if l == nil || l.noop {
		return 0, 0, 0, false
	}
	if rps <= 0 || burst <= 0 {
		return 0, 0, 0, false
	}
	limit = int(burst)

	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[id]
	if b == nil {
		return limit, 0, 0, false
	}
	tokens := b.tokens
	last := b.last
	now := l.now()
	dt := now.Sub(last).Seconds()
	if dt > 0 {
		tokens += dt * rps
		if tokens > burst {
			tokens = burst
		}
	}
	if tokens < 0 {
		tokens = 0
	}
	remaining = int(tokens)
	if remaining < 0 {
		remaining = 0
	}
	if tokens < 1 {
		resetSeconds = int((1-tokens)/rps + 1)
		if resetSeconds < 1 {
			resetSeconds = 1
		}
	}
	return limit, remaining, resetSeconds, true
}

// ForgetAccount drops an account's bucket (ADR-040). Symmetric with Forget
// for the per-account scope. SIGHUP uses ForgetAll which covers both scopes
// at once; ForgetAccount is the per-scope escape hatch (e.g. for an admin
// endpoint that needs to clear one customer's throttle without resetting
// the whole fleet — not wired today, but the seam is here).
func (l *Limiter) ForgetAccount(accountID string) {
	l.mu.Lock()
	l.forgetLocked(accountID)
	l.mu.Unlock()
}

// ForgetAll drops every bucket and returns the count dropped. SIGHUP and the
// apid-side "purge all" callback use this so an operator can recover memory
// after a mass-delete without bouncing the daemon.
//
// Phase 3 (ADR-104): also clears the per-rule consumer-set map so
// the consumer-set bookkeeping resets in lockstep with the bucket
// map. Without this a forgotten rule's consumer set would linger
// and the next request would rebuild a stale collapse bucket
// against the wrong generation of the limiter.
func (l *Limiter) ForgetAll() int {
	l.mu.Lock()
	n := len(l.buckets)
	l.buckets = map[string]*bucket{}
	if l.ll != nil {
		l.ll.Init()
		l.elems = map[string]*list.Element{}
	}
	l.ruleConsumers = map[string]map[string]struct{}{}
	l.mu.Unlock()
	return n
}

// WithNoop returns a new Limiter sharing l's clock + buckets but whose
// Allow always returns true. Test-only seam for the 1k rps hot-path load
// test (handler_load_test.go) which needs to bypass plan rps/burst to
// measure the underlying handler path. DO NOT expose this as a config knob
// — bypassing the limiter would silently turn the §4.1 rate-limit into a
// noop.
//
// The returned limiter shares l's bucket map (read-only after construction
// in tests; no Allow path mutates it). Constructing a fresh mutex is
// intentional — copying a sync.Mutex is a vet error.
//
// LRU state is deliberately NOT carried over: the noop limiter never inserts
// buckets, so leaving cap at zero keeps the recency list out of a path that
// would otherwise have to keep it in lockstep for no benefit.
func (l *Limiter) WithNoop() *Limiter {
	return &Limiter{
		buckets: l.buckets,
		now:     l.now,
		noop:    true,
	}
}

// BucketCount returns the number of buckets currently held. /metrics and the
// SIGHUP observability log use this.
func (l *Limiter) BucketCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// bucketKeys returns a snapshot of every key in the bucket map
// (ADR-104 amendment 5, issue #881 Phase 4 C4). Used by the
// 'rate_limit_changed' invalidator to scan for rule-scoped
// buckets whose key ends with the rule ID. O(n) per call;
// bounded by EdgeRuleCacheCap (10k) + EdgeRuleConsumerCacheCap
// (100k) so acceptable as a degraded-mode invalidation. A
// future PR can give the Limiter a per-suffix Forget helper
// to skip the snapshot allocation.
func (l *Limiter) bucketKeys() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, 0, len(l.buckets))
	for k := range l.buckets {
		out = append(out, k)
	}
	return out
}

// splitCentralKey parses the central-counter triple from the
// colon-separated wire form
//
//	"<scope>:<subject_id>:<plan>"
//
// used by allowTokenWithCentralKey. Returns ok=false for any
// malformation (an empty segment, a missing segment, or a plan
// that isn't in the closed four-plan enum). The closed-vocab
// check defends against a rule compile bug accidentally
// constructing a "scope='something-unknown'" triple that would
// silently bypass the 00126 CHECK at the SQL layer (the SQL
// layer rejects it with 23514, but catching it earlier lets the
// load-bearing 429 path return a 500 instead of leaking the
// rejection past the gateway — the shared-counter key must be semantically
// valid before the central consume).
func splitCentralKey(centralKey string) (scope, subjectID, plan string, ok bool) {
	parts := strings.SplitN(centralKey, ":", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	scope, subjectID, plan = parts[0], parts[1], parts[2]
	if scope == "" || subjectID == "" || plan == "" {
		return "", "", "", false
	}
	switch plan {
	case "free", "hobby", "pro", "scale":
	default:
		return "", "", "", false
	}
	switch scope {
	case rateLimitScopeApp, rateLimitScopeAccount, rateLimitScopeRule:
	default:
		return "", "", "", false
	}
	return scope, subjectID, plan, true
}
