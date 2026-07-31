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
	if !l.Allow("app-a", api.PlanHobby) {
		t.Fatal("first Allow returned false; want true")
	}
	limit, remaining, _, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek after Allow returned ok=false; want true")
	}
	if limit != 100 { // Hobby RateLimitBurst in pkg/api/limits.go
		t.Errorf("Peek limit = %d, want 100", limit)
	}
	if remaining != 99 {
		t.Errorf("Peek remaining = %d, want 99 (Hobby burst 100, one consumed)", remaining)
	}
}

func TestLimiterPeek_ExhaustedBucketSetsReset(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := NewLimiterWithClock(frozenClock(now))
	// Hobby: rps=20, burst=100. Drain via Allow down to 0.
	for i := 0; i < 100; i++ {
		if !l.Allow("app-a", api.PlanHobby) {
			t.Fatalf("Allow #%d returned false; want true", i)
		}
	}
	// 101st Allow returns false — bucket is exhausted.
	if l.Allow("app-a", api.PlanHobby) {
		t.Fatal("101st Allow returned true; want false (exhausted)")
	}
	// Peek should report remaining=0 and reset_seconds >= 1 (ceil of
	// 1/20s).
	limit, remaining, reset, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek on exhausted bucket returned ok=false; want true (bucket exists)")
	}
	if limit != 100 || remaining != 0 {
		t.Errorf("Peek limit/remaining = %d/%d; want 100/0", limit, remaining)
	}
	if reset < 1 {
		t.Errorf("Peek reset = %d; want >= 1 (1/20s ceil)", reset)
	}
}

func TestLimiterPeek_FullBucketNoReset(t *testing.T) {
	// Frozen clock at t0. Allow once — bucket has 99 tokens. Without
	// advancing the clock, Peek must report remaining=99 AND reset=0
	// (bucket is full enough for the next request; no wait needed).
	l := NewLimiterWithClock(frozenClock(time.Unix(1_700_000_000, 0)))
	if !l.Allow("app-a", api.PlanHobby) {
		t.Fatal("Allow returned false; want true")
	}
	_, remaining, reset, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek returned ok=false; want true")
	}
	if remaining != 99 {
		t.Errorf("Peek remaining = %d; want 99", remaining)
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
	l := NewLimiterWithClock(frozenClock(now))
	for i := 0; i < 100; i++ {
		l.Allow("app-a", api.PlanHobby)
	}
	// Refill window: 100 tokens at 20 rps = 5 seconds. Advance 5.5s.
	clock := now
	l.now = func() time.Time { return clock }
	for i := 0; i < 100; i++ {
		l.Allow("app-a", api.PlanHobby)
	}
	clock = clock.Add(5500 * time.Millisecond)
	_, remaining, reset, ok := l.Peek("app-a", api.PlanHobby)
	if !ok {
		t.Fatal("Peek returned ok=false; want true")
	}
	// rps*5.5 = 110 tokens → clamp to burst=100. So remaining = 100.
	if remaining != 100 {
		t.Errorf("Peek remaining = %d after 5.5s refill; want 100 (capped at burst)", remaining)
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
	if !l.Allow("app-a", api.PlanHobby) {
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
	if !l.Allow("app-a", api.PlanHobby) {
		t.Fatal("Allow returned false; want true")
	}
	_, r1, _, _ := l.Peek("app-a", api.PlanHobby)
	_, r2, _, _ := l.Peek("app-a", api.PlanHobby)
	if r1 != r2 {
		t.Errorf("two consecutive Peek disagree: r1=%d r2=%d", r1, r2)
	}
	if r1 != 99 {
		t.Errorf("Peek remaining after first Allow = %d; want 99", r1)
	}
}

func TestLimiterPeekAccount_BasicShape(t *testing.T) {
	// Pro plan: RateLimitPerAccountRPM() = 1000. Test the math:
	// PeekAccount on a fresh bucket returns ok=false; AllowAccount
	// then allows; PeekAccount returns ok=true with limit=1000 and
	// remaining=999 (burst - 1).
	l := NewLimiterWithClock(frozenClock(time.Unix(1_700_000_000, 0)))
	_, _, _, ok := l.PeekAccount("acct-1", api.PlanPro)
	if ok {
		t.Fatal("PeekAccount on fresh bucket returned ok=true; want false")
	}
	if !l.AllowAccount("acct-1", api.PlanPro) {
		t.Fatal("AllowAccount returned false; want true")
	}
	limit, remaining, _, ok := l.PeekAccount("acct-1", api.PlanPro)
	if !ok {
		t.Fatal("PeekAccount after Allow returned ok=false; want true")
	}
	if limit != 1000 {
		t.Errorf("PeekAccount limit = %d; want 1000 (Pro RPM)", limit)
	}
	if remaining != 999 {
		t.Errorf("PeekAccount remaining = %d; want 999", remaining)
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
