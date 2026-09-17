package state

import (
	"context"
	"time"
)

// ReserveMigrationLease mirrors PgStore's insert-or-replace-on-expiry
// semantics. The MemStore mutex provides the same per-instance serialization
// as the PostgreSQL primary key.
func (m *MemStore) ReserveMigrationLease(_ context.Context, lease MigrationLease) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.migrationLeases {
		if existing.InstanceID != lease.InstanceID {
			continue
		}
		if existing.LeaseExpiresAt.After(lease.CreatedAt) {
			return ErrConflict
		}
		delete(m.migrationLeases, existing.LeaseToken)
	}
	m.migrationLeases[lease.LeaseToken] = lease
	return nil
}

func (m *MemStore) CompleteMigrationLease(_ context.Context, leaseToken, memStorageKey, vmstateStorageKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.migrationLeases[leaseToken]
	if !ok {
		return ErrNotFound
	}
	lease.MemStorageKey = memStorageKey
	lease.VMStateStorageKey = vmstateStorageKey
	lease.Pending = false
	m.migrationLeases[leaseToken] = lease
	return nil
}

func (m *MemStore) GetMigrationLease(_ context.Context, instanceID, leaseToken string) (MigrationLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.migrationLeases[leaseToken]
	if !ok || lease.InstanceID != instanceID {
		return MigrationLease{}, ErrNotFound
	}
	return lease, nil
}

func (m *MemStore) GetMigrationLeaseByToken(_ context.Context, leaseToken string) (MigrationLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.migrationLeases[leaseToken]
	if !ok {
		return MigrationLease{}, ErrNotFound
	}
	return lease, nil
}

func (m *MemStore) RenewMigrationLease(_ context.Context, leaseToken, sourceNodeID string, leaseExpiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lease, ok := m.migrationLeases[leaseToken]
	if !ok {
		return ErrNotFound
	}
	if lease.SourceNodeID != sourceNodeID {
		return ErrConflict
	}
	if !time.Now().UTC().Before(lease.LeaseExpiresAt) {
		return ErrMigrationLeaseExpired
	}
	lease.LeaseExpiresAt = leaseExpiresAt.UTC()
	m.migrationLeases[leaseToken] = lease
	return nil
}

func (m *MemStore) DeleteMigrationLease(_ context.Context, leaseToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.migrationLeases[leaseToken]; !ok {
		return ErrNotFound
	}
	delete(m.migrationLeases, leaseToken)
	return nil
}

func (m *MemStore) ListExpiredMigrationLeases(_ context.Context, now time.Time) ([]MigrationLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MigrationLease
	for _, lease := range m.migrationLeases {
		if lease.LeaseExpiresAt.Before(now) {
			out = append(out, lease)
		}
	}
	return out, nil
}

var _ MigrationLeaseStore = (*MemStore)(nil)
