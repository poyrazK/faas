package state

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectAppByWorkload avoids loading the entire project inventory on each
// internal request. Two IDs suffice to reject ambiguous service names (ADR-20261026170512).
func (s *PgStore) ProjectAppByWorkload(ctx context.Context, accountID, projectID, name string) (App, error) {
	ids, err := sqlc.New().ProjectServiceAppIDs(ctx, s.pool, sqlc.ProjectServiceAppIDsParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), WorkloadName: name})
	if err != nil {
		return App{}, fmt.Errorf("resolve project service: %w", mapErr(err))
	}
	if len(ids) == 0 {
		return App{}, projectServiceNotFound(ctx, s.pool, sqlc.ProjectServiceHasDeletedAppParams{
			AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), WorkloadName: name})
	}
	if len(ids) != 1 {
		return App{}, ErrConflict
	}
	app, err := s.AppByID(ctx, uuidString(ids[0]))
	if err != nil {
		return App{}, err
	}
	if app.AccountID != accountID || app.ProjectID != projectID || app.PreviewOfSlug != "" || app.Status == AppDeleted || !api.SameServiceName(app.WorkloadName, name) {
		return App{}, ErrConflict
	}
	return app, nil
}

// projectServiceNotFound reports a project service name no live member holds:
// ErrProjectServiceDeleted while a deleted member's row still holds it, else
// ErrNotFound.
func projectServiceNotFound(ctx context.Context, db sqlc.DBTX, arg sqlc.ProjectServiceHasDeletedAppParams) error {
	deleted, err := sqlc.New().ProjectServiceHasDeletedApp(ctx, db, arg)
	if err != nil {
		return fmt.Errorf("resolve deleted project service: %w", mapErr(err))
	}
	if deleted {
		return ErrProjectServiceDeleted
	}
	return ErrNotFound
}
