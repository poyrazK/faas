package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readEnvironmentRuntimeChangedAt(ctx context.Context, db sqlc.DBTX, appID, scope string) (time.Time, bool, error) {
	at, err := sqlc.New().AppRuntimeConfigChangedAtInScope(ctx, db, sqlc.AppRuntimeConfigChangedAtInScopeParams{
		AppID: mustPgUUID(appID), Scope: normalizedDeploymentScope(scope)})
	if err != nil {
		return time.Time{}, false, mapErr(err)
	}
	return at.Time, at.Valid, nil
}

func (s *PgStore) AppRuntimeConfigChangedAtInScope(ctx context.Context, appID, scope string) (time.Time, bool, error) {
	return readEnvironmentRuntimeChangedAt(ctx, s.pool, appID, scope)
}

func (s *PgStore) InvalidateAppSnapshotsInScope(ctx context.Context, appID, scope string) (int, error) {
	scope = normalizedDeploymentScope(scope)
	if api.ValidateScope(scope) != nil {
		return 0, ErrInvalidArgument
	}
	if scope == "default" {
		return InvalidateAppSnapshots(ctx, s, appID)
	}
	count, err := sqlc.New().InvalidateEnvironmentGitOpsRuntimeConfig(ctx, s.pool,
		sqlc.InvalidateEnvironmentGitOpsRuntimeConfigParams{AppID: mustPgUUID(appID), Scope: scope})
	return int(count), mapErr(err)
}
