package state

import (
	"context"
	"fmt"
	"sort"
)

// PublishServiceCallerKey mirrors the PgStore semantics: one row per node,
// replaced on rotation.
func (m *MemStore) PublishServiceCallerKey(_ context.Context, key ServiceCallerKey) error {
	if key.NodeID == "" || key.KeyID == "" || key.PublicKeyPEM == "" {
		return fmt.Errorf("state: publish service caller key: node, key id and PEM are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.serviceCallerKeys == nil {
		m.serviceCallerKeys = map[string]ServiceCallerKey{}
	}
	m.serviceCallerKeys[key.NodeID] = key
	return nil
}

// ListServiceCallerKeys returns every published key, ordered by node to match
// the PgStore's deterministic ordering.
func (m *MemStore) ListServiceCallerKeys(context.Context) ([]ServiceCallerKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServiceCallerKey, 0, len(m.serviceCallerKeys))
	for _, key := range m.serviceCallerKeys {
		out = append(out, key)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
