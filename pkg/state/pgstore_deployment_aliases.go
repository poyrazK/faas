package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeploymentAliasStore = (*PgStore)(nil)
var _ DeploymentAliasRoutingStore = (*PgStore)(nil)

func (s *PgStore) ListDeploymentAliases(ctx context.Context, appID string) ([]DeploymentAlias, error) {
	rows, err := sqlc.New().ListDeploymentAliases(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	aliases := make([]DeploymentAlias, 0, len(rows))
	for _, row := range rows {
		aliases = append(aliases, DeploymentAlias{
			AppID:        pgUUIDString(row.AppID),
			Name:         row.Name,
			DeploymentID: pgUUIDString(row.DeploymentID),
			Revision:     int(row.Revision),
			CreatedAt:    row.CreatedAt.Time,
			UpdatedAt:    row.UpdatedAt.Time,
		})
	}
	return aliases, nil
}

func (s *PgStore) SetDeploymentAlias(ctx context.Context, appID, name, deploymentID string) (DeploymentAlias, error) {
	if !api.ValidDeploymentAliasName(name) {
		return DeploymentAlias{}, ErrInvalidArgument
	}
	app, err := s.AppByID(ctx, appID)
	if err != nil {
		return DeploymentAlias{}, err
	}
	hostLabel, ok := api.DeploymentAliasHostLabel(app.ID, name)
	if !ok {
		return DeploymentAlias{}, ErrInvalidArgument
	}
	if _, err := s.AppBySlug(ctx, hostLabel); err == nil {
		return DeploymentAlias{}, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return DeploymentAlias{}, err
	}
	row, err := sqlc.New().UpsertDeploymentAlias(ctx, s.pool, sqlc.UpsertDeploymentAliasParams{
		AppID: mustPgUUID(appID), Name: name, DeploymentID: mustPgUUID(deploymentID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return DeploymentAlias{}, ErrNotFound
	}
	if err != nil {
		return DeploymentAlias{}, mapErr(err)
	}
	return DeploymentAlias{
		AppID:        pgUUIDString(row.AppID),
		Name:         row.Name,
		DeploymentID: pgUUIDString(row.DeploymentID),
		Revision:     int(row.Revision),
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}, nil
}

func (s *PgStore) DeploymentAliasByHostLabel(ctx context.Context, hostLabel string) (DeploymentAlias, error) {
	rows, err := sqlc.New().DeploymentAliasByHostLabel(ctx, s.pool, hostLabel)
	if err != nil {
		return DeploymentAlias{}, mapErr(err)
	}
	if len(rows) == 0 {
		return DeploymentAlias{}, ErrNotFound
	}
	if len(rows) != 1 {
		return DeploymentAlias{}, ErrConflict
	}
	row := rows[0]
	return DeploymentAlias{
		AppID:        pgUUIDString(row.AppID),
		Name:         row.Name,
		DeploymentID: pgUUIDString(row.DeploymentID),
		Revision:     int(row.Revision),
		CreatedAt:    row.CreatedAt.Time,
		UpdatedAt:    row.UpdatedAt.Time,
	}, nil
}

func (s *PgStore) DeleteDeploymentAlias(ctx context.Context, appID, name string) error {
	if !api.ValidDeploymentAliasName(name) {
		return ErrInvalidArgument
	}
	count, err := sqlc.New().DeleteDeploymentAlias(ctx, s.pool, sqlc.DeleteDeploymentAliasParams{
		AppID: mustPgUUID(appID), Name: name,
	})
	if err != nil {
		return mapErr(err)
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
