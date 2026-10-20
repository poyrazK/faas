package outbound

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Shared prompt-prefix sightings (ADR 20261019132139): one row per
// integration and prefix hash, never prompt text.

// SeenAIPromptPrefix records a sighting and reports whether the previous one
// fell within lifetime, in one statement.
func (b *PostgresBackend) SeenAIPromptPrefix(ctx context.Context, integrationID string, key [32]byte, lifetime time.Duration) (bool, error) {
	id, err := uuid.Parse(integrationID)
	if err != nil {
		return false, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	var repeated bool
	err = b.pool.QueryRow(ctx, `
		INSERT INTO outbound_ai_prompt_prefixes AS prefix (integration_id, prefix_hash, seen_at)
		VALUES ($1, $2, now())
		ON CONFLICT (integration_id, prefix_hash) DO UPDATE SET previous_seen_at = prefix.seen_at, seen_at = now()
		RETURNING previous_seen_at IS NOT NULL AND previous_seen_at > now() - make_interval(secs => $3::double precision)`,
		id, key[:], lifetime.Seconds()).Scan(&repeated)
	if err != nil {
		return false, fmt.Errorf("record AI prompt prefix: %w", err)
	}
	return repeated, nil
}

// memoryPromptPrefixesMax bounds the memory backend's sightings.
const memoryPromptPrefixesMax = 100_000

type memoryPromptPrefixKey struct {
	integrationID string
	key           [32]byte
}

// SeenAIPromptPrefix records a sighting in memory; it stands in for the
// shared table when several handlers share one backend in tests.
func (b *MemoryBackend) SeenAIPromptPrefix(ctx context.Context, integrationID string, key [32]byte, lifetime time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if b.promptPrefixes == nil || len(b.promptPrefixes) >= memoryPromptPrefixesMax {
		b.promptPrefixes = make(map[memoryPromptPrefixKey]time.Time)
	}
	entry := memoryPromptPrefixKey{integrationID: integrationID, key: key}
	last, ok := b.promptPrefixes[entry]
	b.promptPrefixes[entry] = now
	return ok && now.Sub(last) < lifetime, nil
}

var _ AIPromptPrefixBackend = (*PostgresBackend)(nil)
var _ AIPromptPrefixBackend = (*MemoryBackend)(nil)
