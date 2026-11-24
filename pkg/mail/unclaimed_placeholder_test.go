package mail

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// An unclaimed anonymous-deploy account's placeholder address is never
// mailed, even alongside a real recipient (ADR-20261124080000).
func TestSuppressingSenderDropsUnclaimedPlaceholders(t *testing.T) {
	inner := &recordingSender{}
	checker := newFakeSuppressionChecker()
	s := &SuppressingSender{Inner: inner, Store: checker, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	if err := s.Send(context.Background(), Message{To: []string{"anon-0a1b@Unclaimed.Invalid"}, Subject: "x", TextBody: "x"}); err != nil {
		t.Fatal(err)
	}
	if inner.got.Load() != 0 || checker.calls.Load() != 0 {
		t.Fatalf("placeholder-only message reached the transport (%d) or the store (%d)", inner.got.Load(), checker.calls.Load())
	}

	if err := s.Send(context.Background(), Message{To: []string{"anon-0a1b@unclaimed.invalid", "owner@example.com"}, Subject: "x", TextBody: "x"}); err != nil {
		t.Fatal(err)
	}
	if inner.got.Load() != 1 || len(inner.lastTo) != 1 || inner.lastTo[0] != "owner@example.com" {
		t.Fatalf("mixed message sent to %v (%d sends), want only owner@example.com", inner.lastTo, inner.got.Load())
	}
}
