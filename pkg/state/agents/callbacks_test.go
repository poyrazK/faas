// adr: 20261030125501 §3 — a thread's latest revocation is what apid checks a
// callback token's issue time against.
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
	if _, found, _ := s.CallbacksRevokedAt(ctx, endpoint, "thread-2"); found {
		t.Fatal("another thread reads as revoked")
	}
	if _, err := s.RevokeCallbacks(ctx, account, endpoint, ""); err == nil {
		t.Fatal("an empty thread was revoked")
	}
}
