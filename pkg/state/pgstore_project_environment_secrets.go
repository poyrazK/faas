// adr: 1139
package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) SetProjectEnvironmentSecret(ctx context.Context, secret ProjectEnvironmentSecret, max int) (ProjectEnvironmentSecret, error) {
	if !validProjectEnvironmentSecret(secret, max) {
		return ProjectEnvironmentSecret{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectEnvironmentSecret{}, fmt.Errorf("state: begin project environment secret: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	account, project := mustPgUUID(secret.AccountID), mustPgUUID(secret.ProjectID)
	if _, err := q.LockProjectEnvironmentForSecrets(ctx, tx, sqlc.LockProjectEnvironmentForSecretsParams{
		AccountID: account, ProjectID: project, EnvironmentSlug: secret.EnvironmentSlug,
	}); err != nil {
		return ProjectEnvironmentSecret{}, mapErr(err)
	}
	count, err := q.CountProjectEnvironmentSecrets(ctx, tx, sqlc.CountProjectEnvironmentSecretsParams{
		Key: secret.Key, ProjectID: project, EnvironmentSlug: secret.EnvironmentSlug,
	})
	if err != nil {
		return ProjectEnvironmentSecret{}, mapErr(err)
	}
	if !count.Exists && count.Keys >= int64(max) {
		return ProjectEnvironmentSecret{}, ErrProjectEnvironmentSecretLimit
	}
	row, err := q.UpsertProjectEnvironmentSecret(ctx, tx, sqlc.UpsertProjectEnvironmentSecretParams{
		AccountID: account, ProjectID: project, EnvironmentSlug: secret.EnvironmentSlug, Key: secret.Key,
		Ciphertext: secret.Ciphertext, Kid: secret.Kid, ValueHash: secret.ValueHash,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentSecret{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentSecret{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentSecret{}, fmt.Errorf("state: commit project environment secret: %w", err)
	}
	return ProjectEnvironmentSecret{
		AccountID: pgUUIDString(row.AccountID), ProjectID: pgUUIDString(row.ProjectID), EnvironmentSlug: row.EnvironmentSlug,
		Key: row.Key, Kid: row.Kid, ValueHash: row.ValueHash, SecretVersion: row.SecretVersion,
		CreatedAt: timestamptzToTime(row.CreatedAt), UpdatedAt: timestamptzToTime(row.UpdatedAt),
	}, nil
}

func (s *PgStore) DeleteProjectEnvironmentSecret(ctx context.Context, accountID, projectID, environmentSlug, key string) error {
	deleted, err := sqlc.New().DeleteProjectEnvironmentSecret(ctx, s.pool, sqlc.DeleteProjectEnvironmentSecretParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environmentSlug, Key: key,
	})
	if err != nil {
		return mapErr(err)
	}
	if deleted == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListProjectEnvironmentSecrets(ctx context.Context, accountID, projectID, environmentSlug string) ([]ProjectEnvironmentSecret, error) {
	q := sqlc.New()
	if _, err := q.ProjectEnvironmentIDForSecrets(ctx, s.pool, sqlc.ProjectEnvironmentIDForSecretsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environmentSlug,
	}); err != nil {
		return nil, mapErr(err)
	}
	rows, err := q.ListProjectEnvironmentSecrets(ctx, s.pool, sqlc.ListProjectEnvironmentSecretsParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environmentSlug,
	})
	if err != nil {
		return nil, mapErr(err)
	}
	secrets := make([]ProjectEnvironmentSecret, 0, len(rows))
	for _, row := range rows {
		secrets = append(secrets, ProjectEnvironmentSecret{
			AccountID: pgUUIDString(row.AccountID), ProjectID: pgUUIDString(row.ProjectID), EnvironmentSlug: row.EnvironmentSlug,
			Key: row.Key, Kid: row.Kid, ValueHash: row.ValueHash, SecretVersion: row.SecretVersion,
			CreatedAt: timestamptzToTime(row.CreatedAt), UpdatedAt: timestamptzToTime(row.UpdatedAt),
		})
	}
	return secrets, nil
}
