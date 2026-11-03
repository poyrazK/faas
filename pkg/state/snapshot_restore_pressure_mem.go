package state

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

type snapshotRestorePressureLease struct {
	nodeID    string
	expiresAt time.Time
}

func (m *MemStore) ActiveSnapshotRestoreCounts(ctx context.Context, excludeLeaseIDs []string) (map[string]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	m.snapshotRestoreLeaseMu.Lock()
	defer m.snapshotRestoreLeaseMu.Unlock()
	counts := make(map[string]int)
	for leaseID, lease := range m.snapshotRestoreLeases {
		if !lease.expiresAt.After(now) {
			delete(m.snapshotRestoreLeases, leaseID)
			continue
		}
		if slices.Contains(excludeLeaseIDs, leaseID) {
			continue
		}
		counts[lease.nodeID]++
	}
	return counts, nil
}

func (m *MemStore) ReserveSnapshotRestore(ctx context.Context, leaseID, nodeID string, ttl time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if leaseID == "" || nodeID == "" || ttl <= 0 {
		return nil, errors.New("state: snapshot restore pressure requires lease id, node id and positive ttl")
	}
	m.snapshotRestoreLeaseMu.Lock()
	if m.snapshotRestoreLeases == nil {
		m.snapshotRestoreLeases = make(map[string]snapshotRestorePressureLease)
	}
	m.snapshotRestoreLeases[leaseID] = snapshotRestorePressureLease{
		nodeID: nodeID, expiresAt: time.Now().Add(ttl),
	}
	m.snapshotRestoreLeaseMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.snapshotRestoreLeaseMu.Lock()
			delete(m.snapshotRestoreLeases, leaseID)
			m.snapshotRestoreLeaseMu.Unlock()
		})
	}, nil
}

var _ SnapshotRestorePressureLeases = (*MemStore)(nil)
