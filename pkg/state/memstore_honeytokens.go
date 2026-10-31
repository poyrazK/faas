package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ HoneytokenStore = (*MemStore)(nil)

// CreateHoneytoken mirrors PgStore (ADR 20261024014500).
func (m *MemStore) CreateHoneytoken(_ context.Context, kind, digest, label string) (Honeytoken, error) {
	if !validHoneytoken(kind, digest, label) {
		return Honeytoken{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.honeytokens == nil {
		m.honeytokens = map[string]Honeytoken{}
	}
	if _, dup := m.honeytokens[digest]; dup {
		return Honeytoken{}, ErrConflict
	}
	h := Honeytoken{ID: uuid.NewString(), Kind: kind, Label: label, TokenDigest: digest, PlantedAt: time.Now().UTC()}
	m.honeytokens[digest] = h
	return h, nil
}

// MatchHoneytoken mirrors PgStore.
func (m *MemStore) MatchHoneytoken(_ context.Context, digest string) (Honeytoken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.honeytokens[digest]
	if !ok {
		return Honeytoken{}, ErrNotFound
	}
	return h, nil
}

// ListHoneytokens mirrors PgStore.
func (m *MemStore) ListHoneytokens(_ context.Context) ([]Honeytoken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Honeytoken, 0, len(m.honeytokens))
	for _, h := range m.honeytokens {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PlantedAt.Equal(out[j].PlantedAt) {
			return out[i].PlantedAt.Before(out[j].PlantedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// RetireHoneytoken mirrors PgStore.
func (m *MemStore) RetireHoneytoken(_ context.Context, id string) (Honeytoken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for hash, h := range m.honeytokens {
		if h.ID != id {
			continue
		}
		if h.RetiredAt == nil {
			now := time.Now().UTC()
			h.RetiredAt = &now
			m.honeytokens[hash] = h
		}
		return h, nil
	}
	return Honeytoken{}, ErrNotFound
}
