package state

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func projectCloneValueScopesTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) ([]byte, error) {
	queries := new(sqlc.Queries)
	appIDs, err := queries.LockProjectEnvironmentCloneApps(ctx, tx, sqlc.LockProjectEnvironmentCloneAppsParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	scopes := make(map[string]string, len(appIDs))
	for _, appID := range appIDs {
		scope := clone.SourceSlug
		if scope == "production" {
			selected, err := queries.ReadProjectEnvironmentCloneProductionValueScope(ctx, tx, sqlc.ReadProjectEnvironmentCloneProductionValueScopeParams{
				AppID: mustPgUUID(appID), ProjectID: mustPgUUID(clone.ProjectID),
			})
			if err != nil {
				return nil, mapErr(err)
			}
			if selected == "" {
				return nil, ErrConflict
			}
			scope = selected
		}
		scopes[appID] = scope
	}
	if err := validateCloneValueScopes(scopes, clone.ExpectedSourceValueScopes); err != nil {
		return nil, err
	}
	return json.Marshal(scopes)
}
