package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const workloadSpecSelect = `select s.id::text, e.account_id::text, e.project_id::text,
	s.environment_id::text, e.slug, s.app_id::text, s.revision, s.config_hash, s.settings, s.created_at
	from project_environment_workload_specs s
	join project_environments e on e.id = s.environment_id
	join apps a on a.id = s.app_id and a.project_id = e.project_id and a.account_id = e.account_id
`

func scanWorkloadSpec(row pgx.Row) (ProjectEnvironmentWorkloadSpec, error) {
	var spec ProjectEnvironmentWorkloadSpec
	var raw []byte
	if err := row.Scan(&spec.ID, &spec.AccountID, &spec.ProjectID, &spec.EnvironmentID,
		&spec.EnvironmentSlug, &spec.AppID, &spec.Revision, &spec.Hash, &raw, &spec.CreatedAt); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, mapErr(err)
	}
	if err := json.Unmarshal(raw, &spec.Settings); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	hash, err := WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	return spec, nil
}

func (s *PgStore) ProjectEnvironmentWorkloadSpec(ctx context.Context, accountID, projectID, environment, appID string) (ProjectEnvironmentWorkloadSpec, error) {
	return scanWorkloadSpec(s.pool.QueryRow(ctx, workloadSpecSelect+`
		join project_environment_workload_heads h on h.spec_id = s.id
		where e.account_id = $1 and e.project_id = $2 and e.slug = $3 and s.app_id = $4 and a.status <> 'deleted'`,
		accountID, projectID, environment, appID))
}

func (s *PgStore) ProjectEnvironmentWorkloadSpecByID(ctx context.Context, accountID, projectID, id string) (ProjectEnvironmentWorkloadSpec, error) {
	return scanWorkloadSpec(s.pool.QueryRow(ctx, workloadSpecSelect+`
		where e.account_id = $1 and e.project_id = $2 and s.id = $3 and a.status <> 'deleted'`, accountID, projectID, id))
}

func (s *PgStore) ProjectEnvironmentWorkloadSpecForDeployment(ctx context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentWorkloadSpec, error) {
	return scanWorkloadSpec(s.pool.QueryRow(ctx, workloadSpecSelect+`
		join project_environment_workload_deployment_specs p on p.spec_id = s.id
		join deployments d on d.id = p.deployment_id and d.app_id = s.app_id
		where e.account_id = $1 and e.project_id = $2 and d.id = $3 and a.status <> 'deleted'
		and e.slug = case when d.scope = 'default' then 'production' else d.scope end`, accountID, projectID, deploymentID))
}

func (s *PgStore) PutProjectEnvironmentWorkloadSpec(ctx context.Context, accountID, projectID, environment, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error) {
	return s.putProjectEnvironmentWorkloadSpec(ctx, accountID, projectID, environment, appID, expectedRevision, settings, false)
}

func (s *PgStore) PutUnprotectedProjectEnvironmentWorkloadSpec(ctx context.Context, accountID, projectID, environmentID, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error) {
	return s.putProjectEnvironmentWorkloadSpec(ctx, accountID, projectID, environmentID, appID, expectedRevision, settings, true)
}

func (s *PgStore) putProjectEnvironmentWorkloadSpec(ctx context.Context, accountID, projectID, environment, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings, guarded bool) (ProjectEnvironmentWorkloadSpec, error) {
	if expectedRevision < 0 {
		return ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, fmt.Errorf("state: begin workload spec edit: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var environmentID string
	var protected bool
	// Lock before the first revision exists as well as for subsequent edits.
	// This serializes two create attempts without relying on a missing head row.
	if err := tx.QueryRow(ctx, `select e.id::text, e.protected from project_environments e
		join apps a on a.project_id = e.project_id and a.account_id = e.account_id
		where e.account_id = $1 and e.project_id = $2
		and (($5 and e.id::text = $3) or (not $5 and e.slug = $3))
		and a.id = $4 and a.status <> 'deleted'
		for update of e, a`, accountID, projectID, environment, appID, guarded).Scan(&environmentID, &protected); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, mapErr(err)
	}
	if guarded && protected {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	spec, err := putWorkloadSpecTx(ctx, tx, environmentID, appID, expectedRevision, settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, fmt.Errorf("state: commit workload spec edit: %w", err)
	}
	return spec, nil
}

func putWorkloadSpecTx(ctx context.Context, tx pgx.Tx, environmentID, appID string, expectedRevision int64, settings ProjectEnvironmentWorkloadSettings) (ProjectEnvironmentWorkloadSpec, error) {
	hash, err := WorkloadSettingsHash(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, ErrInvalidArgument
	}
	current, err := scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
		join project_environment_workload_heads h on h.spec_id = s.id
		where s.environment_id = $1 and s.app_id = $2`, environmentID, appID))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	if current.Revision != expectedRevision {
		return ProjectEnvironmentWorkloadSpec{}, ErrConflict
	}
	if current.ID != "" && current.Hash == hash {
		return current, nil
	}
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `insert into project_environment_workload_specs
		(id, environment_id, app_id, revision, config_hash, settings) values ($1, $2, $3, $4, $5, $6)`,
		id, environmentID, appID, expectedRevision+1, hash, raw); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `insert into project_environment_workload_heads (environment_id, app_id, spec_id)
		values ($1, $2, $3) on conflict (environment_id, app_id) do update set spec_id = excluded.spec_id`,
		environmentID, appID, id); err != nil {
		return ProjectEnvironmentWorkloadSpec{}, mapErr(err)
	}
	spec, err := scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`where s.id = $1`, id))
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, err
	}
	return spec, nil
}
