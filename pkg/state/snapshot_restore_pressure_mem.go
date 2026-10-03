package state

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

type snapshotRestorePressureLease struct {
	nodeID    string
	expiresAt time.Time
}

func (m *MemStore) AcquireSnapshotRestorePressure(ctx context.Context) (SnapshotRestorePressureSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.snapshotRestorePressureMu.Lock()
	if err := ctx.Err(); err != nil {
		m.snapshotRestorePressureMu.Unlock()
		return nil, err
	}
	return &memSnapshotRestorePressureSession{store: m}, nil
}

type memSnapshotRestorePressureSession struct {
	store *MemStore
	once  sync.Once
}

func (s *memSnapshotRestorePressureSession) ActiveSnapshotRestoreCounts(ctx context.Context) (map[string]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	s.store.snapshotRestoreLeaseMu.Lock()
	defer s.store.snapshotRestoreLeaseMu.Unlock()
	counts := make(map[string]int)
	for leaseID, lease := range s.store.snapshotRestoreLeases {
		if !lease.expiresAt.After(now) {
			delete(s.store.snapshotRestoreLeases, leaseID)
			continue
		}
		counts[lease.nodeID]++
	}
	return counts, nil
}

func (s *memSnapshotRestorePressureSession) ReserveSnapshotRestore(ctx context.Context, nodeID string, ttl time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nodeID == "" || ttl <= 0 {
		return nil, errors.New("state: snapshot restore pressure requires node id and positive ttl")
	}
	leaseID := uuid.NewString()
	s.store.snapshotRestoreLeaseMu.Lock()
	if s.store.snapshotRestoreLeases == nil {
		s.store.snapshotRestoreLeases = make(map[string]snapshotRestorePressureLease)
	}
	s.store.snapshotRestoreLeases[leaseID] = snapshotRestorePressureLease{
		nodeID: nodeID, expiresAt: time.Now().Add(ttl),
	}
	s.store.snapshotRestoreLeaseMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			s.store.snapshotRestoreLeaseMu.Lock()
			delete(s.store.snapshotRestoreLeases, leaseID)
			s.store.snapshotRestoreLeaseMu.Unlock()
		})
	}, nil
}

func (s *memSnapshotRestorePressureSession) Close() {
	s.once.Do(func() { s.store.snapshotRestorePressureMu.Unlock() })
}

var _ SnapshotRestorePressureCoordinator = (*MemStore)(nil)
var _ SnapshotRestorePressureSession = (*memSnapshotRestorePressureSession)(nil)
