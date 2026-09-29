package state

import (
	"context"
	"time"
)

func (m *MemStore) CreateProjectEnvironmentCloneOperation(_ context.Context, op ProjectEnvironmentCloneOperation) (ProjectEnvironmentCloneOperation, error) {
	if err := validateProjectEnvironmentCloneOperation(op); err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	project, ok := m.projects[op.ProjectID]
	if !ok || project.AccountID != op.AccountID {
		return ProjectEnvironmentCloneOperation{}, ErrNotFound
	}
	for _, existing := range m.projectEnvironmentCloneOperations {
		if existing.AccountID == op.AccountID && existing.ProjectID == op.ProjectID && existing.IdempotencyKey == op.IdempotencyKey {
			if existing.SourceEnvironment == op.SourceEnvironment && existing.TargetEnvironment == op.TargetEnvironment &&
				existing.SourceRevisionHash == op.SourceRevisionHash && existing.SourceReleaseSetID == op.SourceReleaseSetID {
				return cloneProjectEnvironmentCloneOperation(existing), nil
			}
			return ProjectEnvironmentCloneOperation{}, ErrConflict
		}
		if existing.ProjectID == op.ProjectID && existing.TargetEnvironment == op.TargetEnvironment &&
			cloneOperationReservesTarget(existing.Status) {
			return ProjectEnvironmentCloneOperation{}, ErrConflict
		}
	}
	if _, err := m.projectEnvironmentBySlugLocked(op.ProjectID, op.SourceEnvironment); err != nil {
		return ProjectEnvironmentCloneOperation{}, err
	}
	if _, err := m.projectEnvironmentBySlugLocked(op.ProjectID, op.TargetEnvironment); err == nil {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	op.ID = newID()
	if op.Status != "" && op.Status != CloneOperationPending {
		return ProjectEnvironmentCloneOperation{}, ErrInvalidProjectEnvironmentCloneOperation
	}
	op.Status = CloneOperationPending
	op.Revision = 1
	op.Resources = nil
	op.ErrorCode = ""
	op.CreatedAt = time.Now().UTC()
	op.UpdatedAt = op.CreatedAt
	m.projectEnvironmentCloneOperations[op.ID] = op
	return cloneProjectEnvironmentCloneOperation(op), nil
}

func cloneOperationReservesTarget(status string) bool {
	switch status {
	case CloneOperationPending, CloneOperationCapturing, CloneOperationCopying, CloneOperationPublishing, CloneOperationFailed, CloneOperationCompensating:
		return true
	default:
		return false
	}
}

func (m *MemStore) ProjectEnvironmentCloneOperationByID(_ context.Context, accountID, projectID, id string) (ProjectEnvironmentCloneOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[id]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return ProjectEnvironmentCloneOperation{}, ErrNotFound
	}
	return cloneProjectEnvironmentCloneOperation(op), nil
}

func (m *MemStore) ProjectEnvironmentCloneOperationByIdempotencyKey(_ context.Context, accountID, projectID, key string) (ProjectEnvironmentCloneOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, op := range m.projectEnvironmentCloneOperations {
		if op.AccountID == accountID && op.ProjectID == projectID && op.IdempotencyKey == key {
			return cloneProjectEnvironmentCloneOperation(op), nil
		}
	}
	return ProjectEnvironmentCloneOperation{}, ErrNotFound
}

func (m *MemStore) AdvanceProjectEnvironmentCloneOperation(_ context.Context, accountID, projectID, id, expectedStatus, nextStatus string, expectedRevision int64, resources []ProjectEnvironmentCloneResource, errorCode string) (ProjectEnvironmentCloneOperation, error) {
	if expectedRevision < 1 || !validCloneOperationTransition(expectedStatus, nextStatus) || !validCloneOperationErrorCode(errorCode) {
		return ProjectEnvironmentCloneOperation{}, ErrInvalidProjectEnvironmentCloneOperation
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[id]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return ProjectEnvironmentCloneOperation{}, ErrNotFound
	}
	if op.Status != expectedStatus || op.Revision != expectedRevision {
		return ProjectEnvironmentCloneOperation{}, ErrConflict
	}
	op.Status = nextStatus
	op.Revision++
	op.Resources = append([]ProjectEnvironmentCloneResource(nil), resources...)
	op.ErrorCode = errorCode
	op.UpdatedAt = time.Now().UTC()
	m.projectEnvironmentCloneOperations[id] = op
	return cloneProjectEnvironmentCloneOperation(op), nil
}
