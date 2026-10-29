package gateway

import (
	"context"
	"testing"
	"time"
)

// slowCentral answers after delay, or with the consult context's error if its
// deadline comes first, as a Postgres statement does.
type slowCentral struct {
	fakeCentral
	delay time.Duration
}

func (s *slowCentral) ConsumeToken(ctx context.Context, _, _, _ string, _, _ float64) (int, bool, error) {
	s.consumeCalls.Add(1)
	select {
	case <-time.After(s.delay):
		return 10, true, nil
	case <-ctx.Done():
		return 0, false, ctx.Err()
	}
}

// TestLimiter_SlowCentralCommitStillAdmits is the ADR-570 amendment of
// 2026-10-28: a counter write that commits in 400 ms is a verified
// admission, not a 503. Only a consume past centralConsumeTimeout refuses.
func TestLimiter_SlowCentralCommitStillAdmits(t *testing.T) {
	const centralKey = "app:00000000-0000-0000-0000-000000000001:hobby"
	slow := &slowCentral{delay: 400 * time.Millisecond}
	l := NewLimiterWithCentral(slow)
	if !l.AllowWithCentralParams(context.Background(), "appid", 20, 100, centralKey) {
		t.Fatal("a consume that committed in 400 ms was refused")
	}
	if l.centralBreakerOpen(time.Now()) {
		t.Fatal("a slow statement opened the consult breaker")
	}

	stalled := &slowCentral{delay: centralConsumeTimeout + 500*time.Millisecond}
	l = NewLimiterWithCentral(stalled)
	start := time.Now()
	if l.AllowWithCentralParams(context.Background(), "appid", 20, 100, centralKey) {
		t.Fatal("a consume past centralConsumeTimeout was admitted unverified")
	}
	if waited := time.Since(start); waited > centralConsumeTimeout+250*time.Millisecond {
		t.Fatalf("refusal took %s, want about %s", waited, centralConsumeTimeout)
	}
}
