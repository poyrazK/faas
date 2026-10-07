package state

import "context"

var _ MigrationCommitRecoveryStore = (*MemStore)(nil)

func (m *MemStore) ResolveInstanceMigrationCommit(_ context.Context, attempt MigrationCommitAttempt) (MigrationCommitResolution, error) {
	if err := validateMigrationCommitAttempt(attempt); err != nil {
		return MigrationCommitRetained, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
