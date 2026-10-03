package state

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TryAcquireMirrorSlotLease mirrors the Postgres rule-locked reservation
// contract for tests and single-process development stores.
func (m *MemStore) TryAcquireMirrorSlotLease(ctx context.Context, ruleID string, limit int, ttl time.Duration) (string, bool, error) {
	if m == nil {
		return "", false, fmt.Errorf("state: mirror slot lease memstore is not configured")
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if ruleID == "" || limit <= 0 || ttl <= 0 {
		return "", false, fmt.Errorf("state: invalid mirror slot lease policy")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.mirrorRules[ruleID]; !ok {
		return "", false, ErrNotFound
	}
	if m.mirrorSlotLeases == nil {
		m.mirrorSlotLeases = make(map[string]MirrorSlotLease)
	}
	now := time.Now().UTC()
	active := 0
	for id, lease := range m.mirrorSlotLeases {
		if lease.RuleID != ruleID {
			continue
		}
		if !lease.ExpiresAt.After(now) {
			delete(m.mirrorSlotLeases, id)
			continue
		}
		active++
	}
	if active >= limit {
		return "", false, nil
	}
	leaseID := uuid.NewString()
	m.mirrorSlotLeases[leaseID] = MirrorSlotLease{LeaseID: leaseID, RuleID: ruleID, ExpiresAt: now.Add(ttl)}
	return leaseID, true, nil
}

// ReleaseMirrorSlotLease is idempotent and mirrors the PostgreSQL store.
func (m *MemStore) ReleaseMirrorSlotLease(ctx context.Context, ruleID, leaseID string) error {
	if m == nil || ruleID == "" || leaseID == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if lease, ok := m.mirrorSlotLeases[leaseID]; ok && lease.RuleID == ruleID {
		delete(m.mirrorSlotLeases, leaseID)
	}
	return nil
}
