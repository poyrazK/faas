package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// ClaimManagedRealtimeConnectionOwner mirrors the Postgres CAS lease in the
// in-memory test store. A live lease is never replaced by a competing node.
func (m *MemStore) ClaimManagedRealtimeConnectionOwner(_ context.Context, connectionID, endpointID, nodeID string, ttl time.Duration) (ManagedRealtimeConnectionOwner, error) {
	if err := validateRealtimeOwnerLeaseInput(connectionID, endpointID, nodeID, ttl); err != nil {
		return ManagedRealtimeConnectionOwner{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if existing, ok := m.managedRealtimeOwners[connectionID]; ok && existing.LeaseExpiresAt.After(now) {
		return ManagedRealtimeConnectionOwner{}, ErrManagedRealtimeOwnerConflict
	}
	out := ManagedRealtimeConnectionOwner{
		ConnectionID:   connectionID,
		EndpointID:     endpointID,
		NodeID:         nodeID,
		LeaseToken:     uuid.NewString(),
		LeaseExpiresAt: now.Add(ttl),
		UpdatedAt:      now,
	}
	m.managedRealtimeOwners[connectionID] = out
	return out, nil
}

func (m *MemStore) GetManagedRealtimeConnectionOwner(_ context.Context, connectionID, endpointID string) (ManagedRealtimeConnectionOwner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.managedRealtimeOwners[connectionID]
	if !ok || out.EndpointID != endpointID || !out.LeaseExpiresAt.After(time.Now()) {
		return ManagedRealtimeConnectionOwner{}, ErrNotFound
	}
	return out, nil
}

func (m *MemStore) RenewManagedRealtimeConnectionOwner(_ context.Context, connectionID, endpointID, token string, ttl time.Duration) (ManagedRealtimeConnectionOwner, error) {
	if connectionID == "" || endpointID == "" || token == "" || ttl <= 0 {
		return ManagedRealtimeConnectionOwner{}, errors.New("state: connection, endpoint, token, and positive lease TTL are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.managedRealtimeOwners[connectionID]
	now := time.Now()
	if !ok || out.EndpointID != endpointID || out.LeaseToken != token || !out.LeaseExpiresAt.After(now) {
		return ManagedRealtimeConnectionOwner{}, ErrManagedRealtimeOwnerConflict
	}
	out.LeaseExpiresAt = now.Add(ttl)
	out.UpdatedAt = now
	m.managedRealtimeOwners[connectionID] = out
	return out, nil
}

func (m *MemStore) ReleaseManagedRealtimeConnectionOwner(_ context.Context, connectionID, token string) error {
	if connectionID == "" || token == "" {
		return errors.New("state: connection and token are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out, ok := m.managedRealtimeOwners[connectionID]
	if !ok || out.LeaseToken != token {
		return ErrManagedRealtimeOwnerConflict
	}
	delete(m.managedRealtimeOwners, connectionID)
	return nil
}

// PruneExpiredManagedRealtimeConnectionOwners mirrors the bounded Postgres
// cleanup in the in-memory store used by tests and local development.
func (m *MemStore) PruneExpiredManagedRealtimeConnectionOwners(_ context.Context, limit int) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("state: managed realtime owner prune requires a positive limit, got %d", limit)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	expired := make([]ManagedRealtimeConnectionOwner, 0)
	for _, owner := range m.managedRealtimeOwners {
		if !owner.LeaseExpiresAt.After(now) {
			expired = append(expired, owner)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].LeaseExpiresAt.Equal(expired[j].LeaseExpiresAt) {
			return expired[i].ConnectionID < expired[j].ConnectionID
		}
		return expired[i].LeaseExpiresAt.Before(expired[j].LeaseExpiresAt)
	})
	if len(expired) > limit {
		expired = expired[:limit]
	}
	for _, owner := range expired {
		delete(m.managedRealtimeOwners, owner.ConnectionID)
	}
	return int64(len(expired)), nil
}
