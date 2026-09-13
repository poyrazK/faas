package state

import (
	"context"
	"errors"
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
