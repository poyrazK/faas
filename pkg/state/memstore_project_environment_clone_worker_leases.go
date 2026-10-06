package state

import (
	"context"
	"time"
)

func (m *MemStore) ClaimNextProjectEnvironmentClone(_ context.Context, token string, ttl time.Duration) (ProjectEnvironmentCloneLease, error) {
	if !validCloneLeaseToken(token) || !validCloneLeaseDuration(ttl) {
		return ProjectEnvironmentCloneLease{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	var candidate ProjectEnvironmentCloneOperation
	var selected projectEnvironmentCloneLeaseState
	for _, op := range m.projectEnvironmentCloneOperations {
		lease := m.projectEnvironmentCloneWorkerLeases[op.ID]
		if !cloneOperationWorkerEligible(op.Status) || lease.nextAttemptAt.After(now) || lease.until.After(now) {
			continue
		}
		if lease.nextAttemptAt.IsZero() {
			lease.nextAttemptAt = op.CreatedAt
		}
		if candidate.ID == "" || lease.nextAttemptAt.Before(selected.nextAttemptAt) ||
			(lease.nextAttemptAt.Equal(selected.nextAttemptAt) && (op.CreatedAt.Before(candidate.CreatedAt) ||
				(op.CreatedAt.Equal(candidate.CreatedAt) && op.ID < candidate.ID))) {
			candidate, selected = op, lease
		}
	}
	if candidate.ID == "" {
		return ProjectEnvironmentCloneLease{}, ErrNotFound
	}
	candidate.Revision++
	candidate.UpdatedAt = now
	selected.token, selected.until, selected.attemptCount = token, now.Add(ttl), selected.attemptCount+1
	m.projectEnvironmentCloneOperations[candidate.ID] = candidate
	m.projectEnvironmentCloneWorkerLeases[candidate.ID] = selected
	return ProjectEnvironmentCloneLease{Operation: cloneProjectEnvironmentCloneOperation(candidate), Token: token, ExpiresAt: selected.until, AttemptCount: selected.attemptCount}, nil
}

func (m *MemStore) cloneLeaseOwnedLocked(lease ProjectEnvironmentCloneLease, now time.Time) (ProjectEnvironmentCloneOperation, projectEnvironmentCloneLeaseState, error) {
	op, ok := m.projectEnvironmentCloneOperations[lease.Operation.ID]
	stored := m.projectEnvironmentCloneWorkerLeases[op.ID]
	if !ok || op.AccountID != lease.Operation.AccountID || op.ProjectID != lease.Operation.ProjectID {
		return op, stored, ErrNotFound
	}
	if stored.token != lease.Token || !stored.until.After(now) || op.Revision != lease.Operation.Revision ||
		op.Status != lease.Operation.Status || !cloneOperationWorkerEligible(op.Status) {
		return op, stored, ErrConflict
	}
	return op, stored, nil
}

func (m *MemStore) RenewProjectEnvironmentCloneLease(_ context.Context, lease ProjectEnvironmentCloneLease, ttl time.Duration) (ProjectEnvironmentCloneLease, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneLeaseDuration(ttl) {
		return ProjectEnvironmentCloneLease{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	op, stored, err := m.cloneLeaseOwnedLocked(lease, now)
	if err != nil {
		return ProjectEnvironmentCloneLease{}, err
	}
	// A shorter heartbeat must not shorten a still-valid lease.
	if until := now.Add(ttl); until.After(stored.until) {
		stored.until = until
	}
	m.projectEnvironmentCloneWorkerLeases[op.ID] = stored
	return ProjectEnvironmentCloneLease{Operation: cloneProjectEnvironmentCloneOperation(op), Token: stored.token, ExpiresAt: stored.until, AttemptCount: stored.attemptCount}, nil
}

func (m *MemStore) ReleaseProjectEnvironmentCloneLease(_ context.Context, lease ProjectEnvironmentCloneLease, retryAfter time.Duration) error {
	if !validCloneLeaseIdentity(lease) || retryAfter < 0 || retryAfter%time.Microsecond != 0 {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Microsecond)
	op, stored, err := m.cloneLeaseOwnedLocked(lease, now)
	if err != nil {
		return err
	}
	op.Revision++
	op.UpdatedAt = now
	stored.token, stored.until, stored.nextAttemptAt = "", time.Time{}, now.Add(retryAfter)
	m.projectEnvironmentCloneOperations[op.ID] = op
	m.projectEnvironmentCloneWorkerLeases[op.ID] = stored
	return nil
}

func (m *MemStore) cloneOperationLeaseLiveLocked(operationID string) bool {
	lease := m.projectEnvironmentCloneWorkerLeases[operationID]
	return lease.token == "" || lease.until.After(time.Now())
}

func (m *MemStore) clearCloneWorkerLeaseLocked(operationID string) {
	lease := m.projectEnvironmentCloneWorkerLeases[operationID]
	lease.token, lease.until = "", time.Time{}
	m.projectEnvironmentCloneWorkerLeases[operationID] = lease
}
