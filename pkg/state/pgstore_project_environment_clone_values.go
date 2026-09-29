package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) CaptureProjectEnvironmentCloneValues(ctx context.Context, accountID, projectID, source string) (ProjectEnvironmentCloneValuesSnapshot, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return ProjectEnvironmentCloneValuesSnapshot{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	clone := ProjectEnvironmentClone{AccountID: accountID, ProjectID: projectID, SourceSlug: source}
	if err := lockProjectEnvironmentCloneSource(ctx, tx, clone); err != nil {
		return ProjectEnvironmentCloneValuesSnapshot{}, mapProjectCloneSnapshotErr(err)
	}
	clone.sourceValueScopesJSON, err = projectCloneValueScopesTx(ctx, tx, clone)
	if err != nil {
		return ProjectEnvironmentCloneValuesSnapshot{}, mapProjectCloneSnapshotErr(err)
	}
	var scopes map[string]string
	if err := json.Unmarshal(clone.sourceValueScopesJSON, &scopes); err != nil {
		return ProjectEnvironmentCloneValuesSnapshot{}, err
	}
	hash, err := projectCloneValuesHashTx(ctx, tx, clone)
	return ProjectEnvironmentCloneValuesSnapshot{ValueScopes: scopes, Hash: hash}, err
}

func projectCloneValuesHashTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) (string, error) {
	queries := new(sqlc.Queries)
	rows, err := queries.ReadProjectEnvironmentCloneVariables(ctx, tx, sqlc.ReadProjectEnvironmentCloneVariablesParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), ValueScopes: clone.sourceValueScopesJSON,
	})
	if err != nil {
		return "", mapProjectCloneSnapshotErr(err)
	}
	variables := make([]projectCloneVariable, len(rows))
	for i, row := range rows {
		variables[i] = projectCloneVariable{AppID: row.AppID, Scope: row.Scope, Key: row.Key, Value: row.Value}
	}
	sealed, err := queries.ReadProjectEnvironmentCloneSecrets(ctx, tx, sqlc.ReadProjectEnvironmentCloneSecretsParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), ValueScopes: clone.sourceValueScopesJSON,
	})
	if err != nil {
		return "", mapProjectCloneSnapshotErr(err)
	}
	secrets := make([]projectCloneSecret, len(sealed))
	for i, row := range sealed {
		if err := json.Unmarshal(row, &secrets[i]); err != nil {
			return "", fmt.Errorf("decode sealed clone source metadata: %w", err)
		}
	}
	var scopes map[string]string
	if err := json.Unmarshal(clone.sourceValueScopesJSON, &scopes); err != nil {
		return "", err
	}
	return projectCloneValuesHash(scopes, variables, secrets)
}

func mapProjectCloneSnapshotErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "40001" {
		return fmt.Errorf("source changed during clone capture: %w", ErrConflict)
	}
	return mapErr(err)
}
