// adr: 20261030125501 §3 amendment 2026-11-22 — a stored large tool result
// is recorded once per endpoint, thread and message, an endpoint's live bytes
// are bounded, a result is read only by its account, and expired results are
// listed for deletion.
package agents_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestCallbackResults(t *testing.T) {
	pool := open(t)
	s := agents.New(pool)
	ctx := context.Background()
	const endpoint, otherEndpoint = "44444444-4444-4444-4444-444444444444", "55555555-5555-5555-5555-555555555555"
	for _, q := range []string{
		`INSERT INTO accounts (id) VALUES ('` + account + `'), ('` + other + `') ON CONFLICT DO NOTHING`,
		`INSERT INTO managed_realtime_endpoints VALUES ('` + endpoint + `'), ('` + otherEndpoint + `')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	result := func(endpointID, thread, message, sha string, size int) agents.CallbackResult {
		return agents.CallbackResult{AccountID: account, EndpointID: endpointID, ThreadID: thread, MessageID: message,
			SHA256: strings.Repeat(sha, 64), SizeBytes: size, ExpiresAt: now.Add(time.Hour)}
	}
	first, existing, err := s.ReserveCallbackResult(ctx, result(endpoint, "t-1", "m-1", "a", 600), now, 1000)
	if err != nil || existing || first.ID == "" {
		t.Fatalf("reserve = %+v, %v, %v", first, existing, err)
	}
	// A retried post finds its result unchanged, even with other data, so the
	// caller can tell a conflict.
	again, existing, err := s.ReserveCallbackResult(ctx, result(endpoint, "t-1", "m-1", "b", 700), now, 1000)
	if err != nil || !existing || again.ID != first.ID || again.SHA256 != strings.Repeat("a", 64) || again.SizeBytes != 600 {
		t.Fatalf("a retried post = %+v, %v, %v", again, existing, err)
	}
	var limit *agents.LimitError
	if _, _, err := s.ReserveCallbackResult(ctx, result(endpoint, "t-2", "m-1", "c", 500), now, 1000); !errors.As(err, &limit) ||
		limit.Max != 1000 || limit.Observed != 1100 {
		t.Fatalf("past the endpoint's live bytes = %v", err)
	}
	if _, _, err := s.ReserveCallbackResult(ctx, result(otherEndpoint, "t-2", "m-1", "c", 500), now, 1000); err != nil {
		t.Fatalf("another endpoint's bytes are its own: %v", err)
	}

	got, err := s.CallbackResultByID(ctx, account, first.ID)
	if err != nil || got.EndpointID != endpoint || got.ThreadID != "t-1" || got.MessageID != "m-1" || got.SizeBytes != 600 ||
		!got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("read = %+v, %v", got, err)
	}
	if _, err := s.CallbackResultByID(ctx, other, first.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("another account's read = %v, want ErrNotFound", err)
	}
	if _, err := s.CallbackResultByID(ctx, account, "not-a-uuid"); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("a malformed ID = %v", err)
	}

	// An expired result no longer counts against the endpoint and is listed
	// for deletion.
	if expired, err := s.ExpiredCallbackResults(ctx, now.Add(2*time.Hour), 10); err != nil || len(expired) != 2 {
		t.Fatalf("expired = %d, %v", len(expired), err)
	}
	if _, _, err := s.ReserveCallbackResult(ctx, result(endpoint, "t-3", "m-1", "d", 900), now.Add(2*time.Hour),
		1000); err == nil {
		t.Fatal("a result already expired at reservation was recorded")
	}
	later := result(endpoint, "t-3", "m-1", "d", 900)
	later.ExpiresAt = now.Add(3 * time.Hour)
	if _, _, err := s.ReserveCallbackResult(ctx, later, now.Add(2*time.Hour), 1000); err != nil {
		t.Fatalf("expired results freed the endpoint's bytes: %v", err)
	}
	if err := s.DeleteCallbackResult(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CallbackResultByID(ctx, account, first.ID); !errors.Is(err, agents.ErrNotFound) {
		t.Fatalf("a deleted result = %v", err)
	}
}
