package state

import (
	"context"
	"fmt"
)

// APIKeyDisplayPrefixStore records the prefix a customer is shown when a key
// is minted (the first 16 characters of the plaintext) so key listings can
// show the same value. Only the key's SHA-256 is persisted, so before this
// the listing showed a hash-derived identifier that never matched the
// minted prefix. Optional: callers type-assert and fall back to the
// hash-derived identifier for stores without it and for keys minted before
// it existed.
type APIKeyDisplayPrefixStore interface {
	SetAPIKeyDisplayPrefix(ctx context.Context, keyID, prefix string) error
	APIKeyDisplayPrefixes(ctx context.Context, keyIDs []string) (map[string]string, error)
}

// SetAPIKeyDisplayPrefix stores the minted prefix for one key.
func (m *MemStore) SetAPIKeyDisplayPrefix(_ context.Context, keyID, prefix string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.keys[keyID]; !ok {
		return ErrNotFound
	}
	if m.keyDisplayPrefixes == nil {
		m.keyDisplayPrefixes = make(map[string]string)
	}
	m.keyDisplayPrefixes[keyID] = prefix
	return nil
}

// APIKeyDisplayPrefixes returns the recorded prefixes for the given keys;
// keys without one are absent from the map.
func (m *MemStore) APIKeyDisplayPrefixes(_ context.Context, keyIDs []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]string, len(keyIDs))
	for _, id := range keyIDs {
		if prefix, ok := m.keyDisplayPrefixes[id]; ok {
			out[id] = prefix
		}
	}
	return out, nil
}

// SetAPIKeyDisplayPrefix stores the minted prefix for one key.
func (s *PgStore) SetAPIKeyDisplayPrefix(ctx context.Context, keyID, prefix string) error {
	tag, err := s.pool.Exec(ctx, `update api_keys set display_prefix = $2 where id = $1`, keyID, prefix)
	if err != nil {
		return fmt.Errorf("state: set api key display prefix: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// APIKeyDisplayPrefixes returns the recorded prefixes for the given keys;
// keys without one are absent from the map.
func (s *PgStore) APIKeyDisplayPrefixes(ctx context.Context, keyIDs []string) (map[string]string, error) {
	out := make(map[string]string, len(keyIDs))
	if len(keyIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id::text, display_prefix from api_keys where id = any($1::uuid[]) and display_prefix is not null`, keyIDs)
	if err != nil {
		return nil, fmt.Errorf("state: list api key display prefixes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, prefix string
		if err := rows.Scan(&id, &prefix); err != nil {
			return nil, fmt.Errorf("state: scan api key display prefix: %w", err)
		}
		out[id] = prefix
	}
	return out, rows.Err()
}
