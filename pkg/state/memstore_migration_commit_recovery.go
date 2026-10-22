package state

import (
	"context"
	"strings"
)

var _ MigrationCommitRecoveryStore = (*MemStore)(nil)

func (m *MemStore) ResolveInstanceMigrationCommit(ctx context.Context, attempt MigrationCommitAttempt) (MigrationCommitResolution, error) {
	if strings.HasPrefix(attempt.LeaseToken, FunctionMigrationTokenPrefix) {
		return resolveFunctionDestinationCommit(ctx, m, attempt)
	}
	if err := validateMigrationCommitAttempt(attempt); err != nil {
		return MigrationCommitRetained, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migrationPendingLocked(attempt.InstanceID, attempt.LeaseToken, false) {
		return MigrationCommitRetained, ErrMigrationCommitUnresolved
	}
	instance, exists := m.instances[attempt.InstanceID]
	if !exists {
		return MigrationCommitObsolete, nil
	}
	resolution := classifyMigrationCommit(instance, attempt)
	if resolution == MigrationCommitAborted && instance.LeaseToken == attempt.LeaseToken {
		instance.State, instance.LeaseToken, instance.MigrationStartedAt = string(StateParked), "", nil
		m.instances[instance.ID] = instance
	}
	return resolution, nil
}
