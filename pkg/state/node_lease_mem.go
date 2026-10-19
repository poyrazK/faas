package state

import (
	"context"
	"time"
)

var _ NodeLeaseStore = (*MemStore)(nil)

func (m *MemStore) RenewNodeLease(_ context.Context, nodeID, holder string, ttl time.Duration) (NodeLease, error) {
	if err := validateNodeLeaseRenewal(nodeID, holder, ttl); err != nil {
		return NodeLease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.computeNodes[nodeID]; !ok {
		return NodeLease{}, ErrNotFound
	}
	now := m.clock()
	current, held := m.nodeLeases[nodeID]
	next := NodeLease{NodeID: nodeID, Holder: holder, Epoch: 1, RenewedAt: now, ExpiresAt: now.Add(ttl), DBNow: now}
	if held {
		switch {
		case current.Holder == holder:
			next.Epoch = current.Epoch
		case current.ExpiresAt.Before(now):
			next.Epoch = current.Epoch + 1
		default:
			return NodeLease{}, ErrNodeLeaseHeld
		}
	}
	if m.nodeLeases == nil {
		m.nodeLeases = make(map[string]NodeLease)
	}
	m.nodeLeases[nodeID] = next
	return next, nil
}

func (m *MemStore) GetNodeLease(_ context.Context, nodeID string) (NodeLease, error) {
	if err := validateSnapshotRecoveryIDs(nodeID); err != nil {
		return NodeLease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.nodeLeases[nodeID]
	if !ok {
		return NodeLease{}, ErrNotFound
	}
	lease.DBNow = m.clock()
	return lease, nil
}
