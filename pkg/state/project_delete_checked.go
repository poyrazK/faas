package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CheckedProjectDeleteStore serializes customer deletion with stage creation
// and clone reservation. Raw DeleteProject remains for internal compensation.
type CheckedProjectDeleteStore interface {
	DeleteProjectIfNoStages(context.Context, string, string) error
}

func (m *MemStore) DeleteProjectIfNoStages(ctx context.Context, accountID, projectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	project, ok := m.projects[projectID]
	if !ok || project.AccountID != accountID {
		return ErrNotFound
	}
	for _, environment := range m.projectEnvironments {
		if environment.ProjectID == projectID && !IsProductionScope(environment.Slug) {
			return ErrConflict
		}
	}
	for _, operation := range m.projectEnvironmentCloneOperations {
		if operation.ProjectID == projectID && cloneOperationWorkerEligible(operation.Status) {
			return ErrConflict
		}
	}
	return m.deleteProjectLocked(projectID)
}

func (s *PgStore) DeleteProjectIfNoStages(ctx context.Context, accountID, projectID string) error {
	// READ COMMITTED sees stage/clone commits made while acquiring the project
	// lock. Their writers take this same lock before inserting either row.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := guardOrgAccessTx(ctx, tx); err != nil {
		return err
	}
	q := sqlc.New()
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID),
	}); err != nil {
		return mapErr(err)
	}
	stages, err := q.ProjectHasNamedStages(ctx, tx, mustPgUUID(projectID))
	if err != nil {
		return mapErr(err)
	}
	clones, err := q.CountUnfinishedProjectEnvironmentCloneOperationsForProject(ctx, tx, mustPgUUID(projectID))
	if err != nil {
		return mapErr(err)
	}
	if stages || clones > 0 {
		return ErrConflict
	}
	deleted, err := q.DeleteOwnedProject(ctx, tx, sqlc.DeleteOwnedProjectParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID),
	})
	if err != nil {
		return hostingParentDeleteError(err)
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return mapErr(tx.Commit(ctx))
}
