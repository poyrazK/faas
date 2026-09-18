package state

import (
	"context"
	"time"
)

// UpsertJobRegistryCredential stores or replaces the sealed credential for a
// job's image registry. The job/account ownership check mirrors the SQL FK and
// prevents stale job IDs from crossing account boundaries in tests.
func (m *MemStore) UpsertJobRegistryCredential(_ context.Context, accountID, jobID, registry, username string, passwordEncrypted []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok || job.Status == "deleted" || job.AccountID != accountID {
		return ErrNotFound
	}
	key := jobRegistryCredentialKey{jobID: jobID, registry: registry}
	now := time.Now().UTC()
	existing, exists := m.jobRegistryCredentials[key]
	if !exists {
		existing.ID = newUUIDString()
		existing.CreatedAt = now
	}
	existing.AccountID = accountID
	existing.JobID = jobID
	existing.Registry = registry
	existing.Username = username
	existing.PasswordEncrypted = append([]byte(nil), passwordEncrypted...)
	existing.UpdatedAt = now
	m.jobRegistryCredentials[key] = existing
	return nil
}

// GetJobRegistryCredential returns the sealed credential for one job and
// registry, or ErrNotFound when the job/credential is absent or not owned by
// accountID. The ciphertext is copied so callers cannot mutate store state.
func (m *MemStore) GetJobRegistryCredential(_ context.Context, accountID, jobID, registry string) (JobRegistryCredential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[jobID]
	if !ok || job.Status == "deleted" || job.AccountID != accountID {
		return JobRegistryCredential{}, ErrNotFound
	}
	row, ok := m.jobRegistryCredentials[jobRegistryCredentialKey{jobID: jobID, registry: registry}]
	if !ok || row.AccountID != accountID {
		return JobRegistryCredential{}, ErrNotFound
	}
	row.PasswordEncrypted = append([]byte(nil), row.PasswordEncrypted...)
	return row, nil
}

// MarkJobRegistryCredentialUsed records the last successful authenticated
// pull. Missing rows are non-fatal to callers because a concurrent job/account
// deletion may race with completion.
func (m *MemStore) MarkJobRegistryCredentialUsed(_ context.Context, accountID, jobID, registry string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.jobRegistryCredentials[jobRegistryCredentialKey{jobID: jobID, registry: registry}]
	if !ok || row.AccountID != accountID {
		return ErrNotFound
	}
	now := time.Now().UTC()
	row.LastUsedAt = &now
	row.UpdatedAt = now
	m.jobRegistryCredentials[jobRegistryCredentialKey{jobID: jobID, registry: registry}] = row
	return nil
}
