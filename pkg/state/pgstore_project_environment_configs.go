package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func scanProjectEnvironmentConfig(row pgx.Row) (ProjectEnvironmentConfig, error) {
	var config ProjectEnvironmentConfig
	var values []byte
	if err := row.Scan(
		&config.ID, &config.AccountID, &config.ProjectID, &config.EnvironmentSlug,
		&config.Version, &config.ConfigHash, &values, &config.CreatedAt,
	); err != nil {
		return ProjectEnvironmentConfig{}, mapErr(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, values); err != nil {
		return ProjectEnvironmentConfig{}, fmt.Errorf("state: compact project environment config: %w", err)
	}
	config.Values = append(json.RawMessage(nil), compact.Bytes()...)
	return config, nil
}

func (s *PgStore) ProjectEnvironmentConfigLatest(ctx context.Context, accountID, projectID, environmentSlug string) (ProjectEnvironmentConfig, error) {
	row := s.pool.QueryRow(ctx, `
		select c.id, c.account_id, c.project_id, c.environment_slug,
		       c.version, c.config_hash, c.config_json, c.created_at
		  from project_environment_config_versions c
		  join projects p on p.id = c.project_id
		 where p.account_id = $1 and c.project_id = $2 and c.environment_slug = $3
		 order by c.version desc
		 limit 1
	`, accountID, projectID, environmentSlug)
	return scanProjectEnvironmentConfig(row)
}

func (s *PgStore) CreateProjectEnvironmentConfigVersion(ctx context.Context, config ProjectEnvironmentConfig) (ProjectEnvironmentConfig, error) {
	if !json.Valid(config.Values) {
		return ProjectEnvironmentConfig{}, fmt.Errorf("invalid project environment configuration JSON")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProjectEnvironmentConfig{}, fmt.Errorf("state: begin environment config version: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var projectID string
	if err := tx.QueryRow(ctx, `
		select p.id
		  from projects p
		  join project_environments e on e.project_id = p.id
		 where p.id = $1 and p.account_id = $2 and e.slug = $3
		 for update
	`, config.ProjectID, config.AccountID, config.EnvironmentSlug).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ProjectEnvironmentConfig{}, ErrNotFound
		}
		return ProjectEnvironmentConfig{}, mapErr(err)
	}

	var version int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(version), 0) + 1
		  from project_environment_config_versions
		 where project_id = $1 and environment_slug = $2
	`, projectID, config.EnvironmentSlug).Scan(&version); err != nil {
		return ProjectEnvironmentConfig{}, mapErr(err)
	}
	row := tx.QueryRow(ctx, `
		insert into project_environment_config_versions
			(account_id, project_id, environment_slug, version, config_hash, config_json)
		values ($1, $2, $3, $4, $5, $6::jsonb)
		returning id, account_id, project_id, environment_slug, version,
		          config_hash, config_json, created_at
	`, config.AccountID, projectID, config.EnvironmentSlug, version, config.ConfigHash, []byte(config.Values))
	created, err := scanProjectEnvironmentConfig(row)
	if err != nil {
		return ProjectEnvironmentConfig{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentConfig{}, fmt.Errorf("state: commit environment config version: %w", err)
	}
	return created, nil
}
