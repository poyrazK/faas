package outbound

// adr: 20261019132139 — Postgres records prompt-prefix sightings in one
// statement, reports a repeat only within the lifetime, and the hourly prune
// drops sightings older than two lifetimes.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresSharedPromptPrefixes(t *testing.T) {
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	key := promptPrefixKey(f.offer.ID, "claude-sonnet-5-5", []byte("system"))
	lifetime := 5 * time.Minute
	for i, want := range []bool{false, true, true} {
		repeated, err := backend.SeenAIPromptPrefix(ctx, f.offer.ID, key, lifetime)
		if err != nil || repeated != want {
			t.Fatalf("sighting %d = %v, %v; want %v", i+1, repeated, err, want)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_prompt_prefixes SET seen_at = now() - interval '20 minutes',
		previous_seen_at = now() - interval '30 minutes' WHERE integration_id = $1`, f.offer.ID); err != nil {
		t.Fatal(err)
	}
	if repeated, err := backend.SeenAIPromptPrefix(ctx, f.offer.ID, key, lifetime); err != nil || repeated {
		t.Fatalf("a sighting older than the lifetime counted: %v, %v", repeated, err)
	}
	other := promptPrefixKey(f.offer.ID, "claude-sonnet-5-5", []byte("other"))
	if _, err := backend.SeenAIPromptPrefix(ctx, f.offer.ID, other, lifetime); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_prompt_prefixes SET seen_at = now() - interval '11 minutes', previous_seen_at = NULL
		WHERE integration_id = $1 AND prefix_hash = $2`, f.offer.ID, other[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.PruneAIUsage(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_prompt_prefixes WHERE integration_id = $1`, f.offer.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows after prune = %d, %v", rows, err)
	}
	if _, err := backend.SeenAIPromptPrefix(ctx, "claude", key, lifetime); !errors.Is(err, ErrInvalidIntegration) {
		t.Fatalf("a non-UUID integration = %v", err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO outbound_ai_prompt_prefixes (integration_id, prefix_hash) VALUES ($1, '\x00')`, f.offer.ID); err == nil {
		t.Fatal("a short prefix hash was stored")
	}
}
