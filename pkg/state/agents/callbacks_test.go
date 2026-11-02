// adr: 20261030125501 §3 — a thread's latest revocation is what apid checks a
// callback token's issue time against, and both are read from the database's
// clock.
package agents_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestCallbackRevocations(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	const endpoint = "33333333-3333-3333-3333-333333333333"
	if _, err := pool.Exec(ctx, `INSERT INTO managed_realtime_endpoints VALUES ($1)`, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.CallbacksRevokedAt(ctx, endpoint, "thread-1"); err != nil || found {
		t.Fatalf("an unrevoked thread: %v %v", found, err)
	}
	first, err := s.RevokeCallbacks(ctx, account, endpoint, "thread-1")
	if err != nil || first.IsZero() {
		t.Fatalf("revoke: %v %v", first, err)
	}
	if at, found, err := s.CallbacksRevokedAt(ctx, endpoint, "thread-1"); err != nil || !found || !at.Equal(first) {
		t.Fatalf("revoked at %v %v %v, want %v", at, found, err, first)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := s.RevokeCallbacks(ctx, account, endpoint, "thread-1")
	if err != nil || !second.After(first) {
		t.Fatalf("a second revocation %v is not later than %v: %v", second, first, err)
	}
	// apid stamps a token's issue time from CallbackClock, so a revocation
	// and an issue order on the one database clock.
	issued, err := s.CallbackClock(ctx)
	if err != nil || !issued.After(second) {
		t.Fatalf("the clock read after a revocation, %v, is not later than it, %v: %v", issued, second, err)
	}
	third, err := s.RevokeCallbacks(ctx, account, endpoint, "thread-1")
	if err != nil || !third.After(issued) {
		t.Fatalf("a revocation after a clock read, %v, is not later than it, %v: %v", third, issued, err)
	}
	if _, found, _ := s.CallbacksRevokedAt(ctx, endpoint, "thread-2"); found {
		t.Fatal("another thread reads as revoked")
	}
	if _, err := s.RevokeCallbacks(ctx, account, endpoint, ""); err == nil {
		t.Fatal("an empty thread was revoked")
	}
}
