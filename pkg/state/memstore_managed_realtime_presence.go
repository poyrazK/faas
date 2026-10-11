package state

import (
	"bytes"
	"context"
	"sort"
	"time"
)

func (m *MemStore) UpsertManagedRealtimePresenceLease(ctx context.Context, lease ManagedRealtimePresenceLease) ([]ManagedRealtimePresenceLease, error) {
	if err := validateManagedRealtimePresenceLease(lease); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[lease.EndpointID]; !ok {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	for key, existing := range m.managedRealtimePresence {
		if key.endpointID == lease.EndpointID && key.channel == lease.Channel && !existing.ExpiresAt.After(now) {
			delete(m.managedRealtimePresence, key)
		}
	}
	key := managedRealtimePresenceKey{endpointID: lease.EndpointID, channel: lease.Channel, nodeID: lease.NodeID, connectionID: lease.ConnectionID}
	if _, exists := m.managedRealtimePresence[key]; !exists {
		count := 0
		for candidate := range m.managedRealtimePresence {
			if candidate.endpointID == lease.EndpointID && candidate.channel == lease.Channel {
				count++
			}
		}
		if count >= ManagedRealtimePresenceMaxMembers {
			return nil, ErrManagedRealtimePresenceLimit
		}
	}
	if lease.Principal != "" {
		for candidate, existing := range m.managedRealtimePresence {
			if candidate.endpointID == lease.EndpointID && candidate.channel == lease.Channel && existing.Principal == lease.Principal && existing.ExpiresAt.After(now) {
				lease.MemberID = existing.MemberID
				break
			}
		}
	}
	if existing, exists := m.managedRealtimePresence[key]; exists && bytes.Equal(existing.State, lease.State) &&
		existing.Principal == lease.Principal && existing.MemberID == lease.MemberID {
		lease.StateUpdatedAt = existing.StateUpdatedAt
	} else {
		lease.StateUpdatedAt = now
	}
	lease.UpdatedAt = now
	lease.State = append([]byte(nil), lease.State...)
	m.managedRealtimePresence[key] = lease
	return m.managedRealtimePresenceSnapshotLocked(lease.EndpointID, lease.Channel, now), nil
}

func (m *MemStore) ReadManagedRealtimePresenceSnapshot(ctx context.Context, endpointID, channel string) ([]ManagedRealtimePresenceLease, error) {
	if validateManagedRealtimeHistoryRequest(endpointID, channel) != nil {
		return nil, ErrManagedRealtimePresenceInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.managedRealtimeEndpoints[endpointID]; !ok {
		return nil, ErrNotFound
	}
	now := time.Now().UTC()
	for key, lease := range m.managedRealtimePresence {
		if key.endpointID == endpointID && key.channel == channel && !lease.ExpiresAt.After(now) {
			delete(m.managedRealtimePresence, key)
		}
	}
	return m.managedRealtimePresenceSnapshotLocked(endpointID, channel, now), nil
}

func (m *MemStore) DeleteManagedRealtimePresenceLease(ctx context.Context, endpointID, channel, nodeID, connectionID string) ([]ManagedRealtimePresenceLease, time.Time, error) {
	if err := validateManagedRealtimePresenceKey(endpointID, channel, nodeID, connectionID); err != nil {
		return nil, time.Time{}, err
	}
	if err := ctx.Err(); err != nil {
		return nil, time.Time{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.managedRealtimePresence, managedRealtimePresenceKey{endpointID: endpointID, channel: channel, nodeID: nodeID, connectionID: connectionID})
	now := time.Now().UTC()
	return m.managedRealtimePresenceSnapshotLocked(endpointID, channel, now), now, nil
}

func (m *MemStore) PruneExpiredManagedRealtimePresenceLeases(ctx context.Context, batch int) (int64, error) {
	if batch < 1 || batch > 1000 {
		return 0, ErrManagedRealtimePresenceInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var removed int64
	for key, lease := range m.managedRealtimePresence {
		if !lease.ExpiresAt.After(now) {
			delete(m.managedRealtimePresence, key)
			removed++
			if removed == int64(batch) {
				break
			}
		}
	}
	return removed, nil
}

func (m *MemStore) managedRealtimePresenceSnapshotLocked(endpointID, channel string, now time.Time) []ManagedRealtimePresenceLease {
	type aggregate struct {
		lease ManagedRealtimePresenceLease
		count int
	}
	grouped := make(map[string]aggregate)
	for key, lease := range m.managedRealtimePresence {
		if key.endpointID == endpointID && key.channel == channel && lease.ExpiresAt.After(now) {
			current, exists := grouped[lease.MemberID]
			if !exists || lease.StateUpdatedAt.After(current.lease.StateUpdatedAt) ||
				(lease.StateUpdatedAt.Equal(current.lease.StateUpdatedAt) && lease.ConnectionID < current.lease.ConnectionID) {
				lease.State = append([]byte(nil), lease.State...)
				current.lease = lease
			}
			current.count++
			grouped[lease.MemberID] = current
		}
	}
	result := make([]ManagedRealtimePresenceLease, 0, len(grouped))
	for _, value := range grouped {
		value.lease.NodeID = ""
		value.lease.ConnectionID = ""
		value.lease.ConnectionCount = value.count
		value.lease.UpdatedAt = now
		result = append(result, value.lease)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].MemberID < result[j].MemberID })
	return result
}
