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
	if lease.SourceNodeID != "" {
		if n, ok := m.computeNodes[lease.SourceNodeID]; ok && (n.Lifecycle == NodeLifecycleMaintenance || n.Lifecycle == NodeLifecycleRetired) {
			return ErrConflict
		}
	}
	for _, existing := range m.migrationLeases {
		if existing.InstanceID != lease.InstanceID {
			continue
		}
		_, sourceReceipt := m.migrationSources[existing.LeaseToken]
		if m.migrationPendingLocked(existing.InstanceID, existing.LeaseToken, false) || sourceReceipt {
			return ErrConflict
		}
		if existing.LeaseExpiresAt.After(lease.CreatedAt) || m.functionMigrationSources[existing.InstanceID].LeaseToken != "" {
			return ErrConflict
		}
		delete(m.migrationLeases, existing.LeaseToken)
		delete(m.migrationResumeFences, existing.LeaseToken)
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
	if _, bound := m.migrationSources[leaseToken]; bound && (lease.MemStorageKey != "" && lease.MemStorageKey != memStorageKey || lease.VMStateStorageKey != "" && lease.VMStateStorageKey != vmstateStorageKey) {
		return ErrConflict
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
	lease, ok := m.migrationLeases[leaseToken]
	if !ok {
		return ErrNotFound
	}
	if m.migrationPendingLocked(lease.InstanceID, leaseToken, false) || m.migrationSourcePendingLocked(leaseToken) {
		return ErrConflict
	}
	if d, ok := m.functionMigrationDestinations[leaseToken]; ok && d.Phase != FunctionDestinationRetired && d.Phase != FunctionDestinationCommitted {
		return ErrConflict
	}
	delete(m.functionMigrationSources, lease.InstanceID)
	delete(m.functionMigrationPermissions, leaseToken)
	delete(m.migrationLeases, leaseToken)
	delete(m.migrationResumeFences, leaseToken)
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
