package state

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// PublishServiceCallerKey mirrors the PgStore semantics: one current key per
// node, with the old key trusted until the maximum assertion TTL elapses.
func (m *MemStore) PublishServiceCallerKey(_ context.Context, key ServiceCallerKey) error {
	if key.NodeID == "" || key.KeyID == "" || key.PublicKeyPEM == "" {
		return fmt.Errorf("state: publish service caller key: node, key id and PEM are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.serviceCallerKeys == nil {
		m.serviceCallerKeys = map[string]ServiceCallerKey{}
	}
	if m.serviceCallerKeyHistory == nil {
		m.serviceCallerKeyHistory = map[string]retiredServiceCallerKey{}
	}
	if current, ok := m.serviceCallerKeys[key.NodeID]; ok {
		if current.KeyID == key.KeyID {
			return nil
		}
		m.serviceCallerKeyHistory[current.KeyID] = retiredServiceCallerKey{
			key:      current,
			retireAt: time.Now().Add(serviceCallerKeyRotationGrace),
		}
	}
	// Restoring a previously used node key makes it current again; it must
	// not also appear as a retired entry.
	delete(m.serviceCallerKeyHistory, key.KeyID)
	m.serviceCallerKeys[key.NodeID] = key
	return nil
}

// ListServiceCallerKeys returns every current key and any recently rotated
// keys, ordered by node and key id to match the PgStore's deterministic order.
func (m *MemStore) ListServiceCallerKeys(context.Context) ([]ServiceCallerKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := make([]ServiceCallerKey, 0, len(m.serviceCallerKeys)+len(m.serviceCallerKeyHistory))
	for _, key := range m.serviceCallerKeys {
		out = append(out, key)
	}
	for keyID, retired := range m.serviceCallerKeyHistory {
		if !now.Before(retired.retireAt) {
			delete(m.serviceCallerKeyHistory, keyID)
			continue
		}
		out = append(out, retired.key)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].KeyID < out[j].KeyID
	})
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
