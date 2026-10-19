package state

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectAppByWorkload avoids loading the entire project inventory on each
// internal request. Two IDs suffice to reject ambiguous case-folded aliases.
func (s *PgStore) ProjectAppByWorkload(ctx context.Context, accountID, projectID, name string) (App, error) {
	ids, err := sqlc.New().ProjectServiceAppIDs(ctx, s.pool, sqlc.ProjectServiceAppIDsParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), WorkloadName: name})
	if err != nil {
		return App{}, fmt.Errorf("resolve project service: %w", mapErr(err))
	}
	if len(ids) == 0 {
		return App{}, ErrNotFound
	}
	if len(ids) != 1 {
		return App{}, ErrConflict
	}
	app, err := s.AppByID(ctx, uuidString(ids[0]))
	if err != nil {
		return App{}, err
	}
	if app.AccountID != accountID || app.ProjectID != projectID || app.PreviewOfSlug != "" || app.Status == AppDeleted || !strings.EqualFold(app.WorkloadName, name) {
		return App{}, ErrConflict
	}
	return app, nil
}
