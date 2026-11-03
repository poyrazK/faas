package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RestoreCopiesStore = (*PostgresStore)(nil)

// HasRestoreCopies implements RestoreCopiesStore.
func (s *PostgresStore) HasRestoreCopies(ctx context.Context, databaseID string) (bool, error) {
	id, err := postgresUUID(databaseID)
	if err != nil {
		return false, err
	}
	dependants, err := new(sqlc.Queries).ReadManagedPostgresLifecycleDependants(ctx, s.catalog, id)
	if err != nil {
		return false, mapPostgresError(err)
	}
	return dependants.HasRestoreDescendants, nil
}

// lifecycleDependantsError refuses a delete claim. Restore copies do not:
// the delete keeps the source's provider resource until they are gone
// (ADR-20261103091657).
func lifecycleDependantsError(dependants sqlc.ReadManagedPostgresLifecycleDependantsRow) error {
	switch {
	case dependants.HasBindings:
		return errDatabaseHasBindings
	case dependants.HasCloneSnapshotHolds || dependants.HasCloneWriteFenceHolds:
		return errDatabaseCloneHold
	}
	return nil
}
