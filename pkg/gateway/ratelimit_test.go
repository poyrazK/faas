// Tests for Limiter.Peek and Limiter.PeekAccount — Finding 6.
//
// The non-mutating read is the load-bearing contract for two
// surfaces: (a) the X-RateLimit-{Limit,Remaining,Reset} response
// headers on every proxied request, and (b) the /v1/internal/quota
// dashboard endpoint. Both rely on Peek returning the same value the
// next Allow would have consumed — without Peek itself consuming a
// token. These tests pin the value math (token floor, reset seconds
// ceil), the visibility gates (nil receiver, noop, unknown plan,
// missing bucket), and the cross-method invariant (Peek == Allow's
// next-token state).
package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// frozenClock returns a deterministic time.Time pointer the limiter
// uses for refill math. Pinned so refill formula behaves predictably
// without flake.
func frozenClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestLimiterPeek_FreshBucket(t *testing.T) {
	l := NewLimiterWithClock(frozenClock(time.Unix(1_700_000_000, 0)))
	// No Allow yet — bucket is nil. Peek returns ok=false.
	_, _, _, ok := l.Peek("app-a", api.PlanHobby)
	if ok {
		t.Fatal("Peek on fresh bucket returned ok=true; want false")
	}
	// Allow consumes 1, but bucket is now non-nil and has burst-1
	// tokens left. Peek should report ok=true and remaining = burst-1.
	if !l.Allow(context.Background(), "app-a", api.PlanHobby) {
		t.Fatal("first Allow returned false; want true")
	}
	limit, remaining, _, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek after Allow returned ok=false; want true")
	}
	burst := api.MustLimitsFor(api.PlanHobby).RateLimitBurst
	if limit != burst {
		t.Errorf("Peek limit = %d, want %d", limit, burst)
	}
	if remaining != burst-1 {
		t.Errorf("Peek remaining = %d, want %d (Hobby burst, one consumed)", remaining, burst-1)
	}
}

func TestLimiterAppRequestRateOverrideChangesPolicyWithoutResettingBucket(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithClock(func() time.Time { return now })
	if !l.AllowAppWithLimits(context.Background(), "app-policy", api.PlanPro, 1, 2) {
		t.Fatal("first overridden Allow returned false")
	}
	// One token remains from the configured burst. Raising the burst at the
	// same instant must not mint the newly available capacity.
	if !l.AllowAppWithLimits(context.Background(), "app-policy", api.PlanPro, 2, 4) {
		t.Fatal("policy change discarded the existing token")
	}
	if l.AllowAppWithLimits(context.Background(), "app-policy", api.PlanPro, 2, 4) {
		t.Fatal("policy change reset the depleted bucket")
	}
	limit, remaining, _, ok := l.Peek("app-policy", api.PlanPro)
	if !ok || limit != 4 || remaining != 0 {
		t.Fatalf("updated bucket snapshot = (%d, %d, %v), want (4, 0, true)", limit, remaining, ok)
	}

	now = now.Add(500 * time.Millisecond)
	if !l.AllowAppWithLimits(context.Background(), "app-policy", api.PlanPro, 2, 4) {
		t.Fatal("new RPS was not applied to refill after the policy change")
	}
	if l.AllowAppWithLimits(context.Background(), "app-policy", api.PlanPro, 0, 0) {
		t.Fatal("restoring plan defaults reset the depleted bucket")
	}
	limit, _, _, ok = l.Peek("app-policy", api.PlanPro)
	if !ok || limit != api.MustLimitsFor(api.PlanPro).RateLimitBurst {
		t.Fatalf("default-restored bucket limit = %d (ok=%v), want %d", limit, ok, api.MustLimitsFor(api.PlanPro).RateLimitBurst)
	}
	now = now.Add(10 * time.Millisecond)
	if !l.AllowAppWithLimits(context.Background(), "app-policy", api.PlanPro, 0, 0) {
		t.Fatal("plan-default refill did not apply after restore")
	}
}

func TestLimiterPeek_ExhaustedBucketSetsReset(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithClock(frozenClock(now))
	// Hobby: drain the burst via Allow down to 0.
	burst := api.MustLimitsFor(api.PlanHobby).RateLimitBurst
	for i := 0; i < burst; i++ {
		if !l.Allow(context.Background(), "app-a", api.PlanHobby) {
			t.Fatalf("Allow #%d returned false; want true", i)
		}
	}
	// The next Allow returns false — bucket is exhausted.
	if l.Allow(context.Background(), "app-a", api.PlanHobby) {
		t.Fatal("Allow past the burst returned true; want false (exhausted)")
	}
	// Peek should report remaining=0 and reset_seconds >= 1 (ceil of
	// 1/rps seconds).
	limit, remaining, reset, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek on exhausted bucket returned ok=false; want true (bucket exists)")
	}
	if limit != burst || remaining != 0 {
		t.Errorf("Peek limit/remaining = %d/%d; want %d/0", limit, remaining, burst)
	}
	if reset < 1 {
		t.Errorf("Peek reset = %d; want >= 1 (ceil of 1/rps seconds)", reset)
	}
}

func TestLimiterPeek_FullBucketNoReset(t *testing.T) {
	// Frozen clock at t0. Allow once — bucket has burst-1 tokens. Without
	// advancing the clock, Peek must report remaining=burst-1 AND reset=0
	// (bucket is full enough for the next request; no wait needed).
	l := NewLimiterWithClock(frozenClock(time.Unix(1_700_000_000, 0)))
	if !l.Allow(context.Background(), "app-a", api.PlanHobby) {
		t.Fatal("Allow returned false; want true")
	}
	_, remaining, reset, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek returned ok=false; want true")
	}
	if want := api.MustLimitsFor(api.PlanHobby).RateLimitBurst - 1; remaining != want {
		t.Errorf("Peek remaining = %d; want %d", remaining, want)
	}
	if reset != 0 {
		t.Errorf("Peek reset = %d on full bucket; want 0 (no wait needed)", reset)
	}
}

func TestLimiterPeek_RefillAfterClockAdvance(t *testing.T) {
	// Drain Hobby's bucket down to 0, then advance the clock by
	// refill-window seconds so the bucket refills; Peek should
	// report remaining > 0 (and reset = 0).
	now := time.Unix(1_700_000_000, 0)
	hobby := api.MustLimitsFor(api.PlanHobby)
	l := NewLimiterWithClock(frozenClock(now))
	for i := 0; i < hobby.RateLimitBurst; i++ {
		l.Allow(context.Background(), "app-a", api.PlanHobby)
	}
	// Refill window: burst/rps seconds. Advance 1.1x that.
	clock := now
	l.now = func() time.Time { return clock }
	for i := 0; i < hobby.RateLimitBurst; i++ {
		l.Allow(context.Background(), "app-a", api.PlanHobby)
	}
	clock = clock.Add(time.Duration(hobby.RateLimitBurst) * 1100 * time.Millisecond / time.Duration(hobby.RateLimitRPS))
	_, remaining, reset, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek returned ok=false; want true")
	}
	// 1.1x the burst refills → clamped to the burst.
	if remaining != hobby.RateLimitBurst {
		t.Errorf("Peek remaining = %d after refill; want %d (capped at burst)", remaining, hobby.RateLimitBurst)
	}
	if reset != 0 {
		t.Errorf("Peek reset = %d on capped bucket; want 0", reset)
	}
}

func TestLimiterPeek_NilReceiverReturnsFalse(t *testing.T) {
	var l *Limiter
	_, _, _, ok := l.Peek("app-a", api.PlanHobby)
	if ok {
		t.Fatal("Peek on nil receiver returned ok=true; want false")
	}
}

func TestLimiterPeek_NoopLimiterReturnsFalse(t *testing.T) {
	l := NewLimiter().WithNoop()
	_, _, _, ok := l.Peek("app-a", api.PlanHobby)
	if ok {
		t.Fatal("Peek on noop limiter returned ok=true; want false (no bucket state)")
	}
}

func TestLimiterPeek_UnknownPlanReturnsFalse(t *testing.T) {
	l := NewLimiter()
	_, _, _, ok := l.Peek("app-a", api.Plan("unknown"))
	if ok {
		t.Fatal("Peek on unknown plan returned ok=true; want false")
	}
}

func TestLimiterPeek_ForgetClearsBucket(t *testing.T) {
	l := NewLimiterWithClock(frozenClock(time.Unix(1_700_000_000, 0)))
	if !l.Allow(context.Background(), "app-a", api.PlanHobby) {
		t.Fatal("Allow returned false; want true")
	}
	// Forget drops the bucket — subsequent Peek should return ok=false.
	l.Forget("app-a")
	_, _, _, ok := l.Peek("app-a", api.PlanHobby)
	if ok {
		t.Fatal("Peek after Forget returned ok=true; want false (bucket dropped)")
	}
}

func TestLimiterPeek_NonMutating(t *testing.T) {
	// Pin that two consecutive Peek calls return the same value AND
	// that an Allow sandwiched between them consumes exactly one
	// token (Peek does NOT).
	now := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithClock(frozenClock(now))
	if !l.Allow(context.Background(), "app-a", api.PlanHobby) {
		t.Fatal("Allow returned false; want true")
	}
	_, r1, _, _ := l.Peek("app-a", api.PlanHobby)
	_, r2, _, _ := l.Peek("app-a", api.PlanHobby)
	if r1 != r2 {
		t.Errorf("two consecutive Peek disagree: r1=%d r2=%d", r1, r2)
	}
	if want := api.MustLimitsFor(api.PlanHobby).RateLimitBurst - 1; r1 != want {
		t.Errorf("Peek remaining after first Allow = %d; want %d", r1, want)
	}
}

// adr: 040
func TestLimiterPeekAccount_BasicShape(t *testing.T) {
	// Pro plan: PeekAccount on a fresh bucket returns ok=false;
	// AllowAccount then allows; PeekAccount returns ok=true with
	// limit=RateLimitPerAccountRPM and remaining=burst-1.
	rpm := api.PlanPro.RateLimitPerAccountRPM()
	l := NewLimiterWithClock(frozenClock(time.Unix(1_700_000_000, 0)))
	_, _, _, ok := l.PeekAccount("acct-1", api.PlanPro)
	if ok {
		t.Fatal("PeekAccount on fresh bucket returned ok=true; want false")
	}
	if !l.AllowAccount(context.Background(), "acct-1", api.PlanPro) {
		t.Fatal("AllowAccount returned false; want true")
	}
	limit, remaining, _, ok := l.PeekAccount("acct-1", api.PlanPro)
	if !ok {
		t.Fatal("PeekAccount after Allow returned ok=false; want true")
	}
	if limit != rpm {
		t.Errorf("PeekAccount limit = %d; want %d (Pro RPM)", limit, rpm)
	}
	if remaining != rpm-1 {
		t.Errorf("PeekAccount remaining = %d; want %d", remaining, rpm-1)
	}
}

func TestLimiterPeekAccount_NilAndNoop(t *testing.T) {
	var lnil *Limiter
	_, _, _, ok := lnil.PeekAccount("acct-1", api.PlanPro)
	if ok {
		t.Fatal("PeekAccount on nil receiver returned ok=true; want false")
	}
	lnoop := NewLimiter().WithNoop()
	_, _, _, ok = lnoop.PeekAccount("acct-1", api.PlanPro)
	if ok {
		t.Fatal("PeekAccount on noop limiter returned ok=true; want false")
	}
}

func TestLimiterPeekAccount_UnknownPlan(t *testing.T) {
	l := NewLimiter()
	_, _, _, ok := l.PeekAccount("acct-1", api.Plan("unknown"))
	if ok {
		t.Fatal("PeekAccount on unknown plan returned ok=true; want false")
	}
}
