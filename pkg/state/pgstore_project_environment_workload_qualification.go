package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) ProjectEnvironmentWorkloadConfigHashes(ctx context.Context, accountID, projectID, environment, releaseSetID string) (map[string]string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	hashes, scoped, err := projectEnvironmentWorkloadConfigHashesTx(ctx, tx, accountID, projectID, environment, releaseSetID)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, mapErr(err)
	}
	return hashes, scoped, nil
}

func projectEnvironmentWorkloadConfigHashesTx(ctx context.Context, tx pgx.Tx, accountID, projectID, environment, releaseSetID string) (map[string]string, bool, error) {
	var environmentID string
	if err := tx.QueryRow(ctx, `select id::text from project_environments
		where account_id = $1 and project_id = $2 and slug = $3 for share`, accountID, projectID, environment).Scan(&environmentID); err != nil {
		return nil, false, mapErr(err)
	}
	var found int
	if err := tx.QueryRow(ctx, `select 1 from project_release_sets
		where id = $1 and account_id = $2 and project_id = $3 and environment_slug = $4 and active for share`,
		releaseSetID, accountID, projectID, environment).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrConflict
		}
		return nil, false, mapErr(err)
	}
	rows, err := tx.Query(ctx, `select `+appsSelectColumns+` from apps
		where account_id = $1 and project_id = $2 and status <> 'deleted'
		and id in (select app_id from project_release_members where release_id = $3)
		order by id for share`, accountID, projectID, releaseSetID)
	if err != nil {
		return nil, false, mapErr(err)
	}
	apps, err := scanApps(rows)
	rows.Close()
	if err != nil {
		return nil, false, err
	}
	hashes := make(map[string]string, len(apps))
	scoped := false
	for _, app := range apps {
		desired, err := workloadSpecOrZero(scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
			join project_environment_workload_heads h on h.spec_id = s.id
			where s.environment_id = $1 and s.app_id = $2`, environmentID, app.ID)))
		if err != nil {
			return nil, false, err
		}
		pinned, err := workloadSpecOrZero(scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
			join project_environment_workload_deployment_specs pin on pin.spec_id = s.id
			join project_release_members member on member.deployment_id = pin.deployment_id
			where member.release_id = $1 and member.app_id = $2`, releaseSetID, app.ID)))
		if err != nil {
			return nil, false, err
		}
		if pinned.ID != "" && (pinned.AppID != app.ID || pinned.EnvironmentID != environmentID) {
			return nil, false, ErrConflict
		}
		legacy, err := materializeWorkloadSettingsTx(ctx, tx, app, environment)
		if err != nil {
			return nil, false, err
		}
		hash, err := qualificationWorkloadHash(desired, pinned, legacy)
		if err != nil {
			return nil, false, err
		}
		hashes[app.Slug] = hash
		scoped = scoped || desired.ID != ""
	}
	if len(hashes) == 0 {
		return nil, false, ErrConflict
	}
	return hashes, scoped, nil
}

func materializeWorkloadSettingsTx(ctx context.Context, tx pgx.Tx, app App, environment string) (ProjectEnvironmentWorkloadSettings, error) {
	settings, err := WorkloadSettingsFromApp(app)
	if err != nil {
		return ProjectEnvironmentWorkloadSettings{}, err
	}
	var routes []byte
	var onlyDeclared bool
	err = tx.QueryRow(ctx, `select only_allow_declared_routes, declared_routes
		from project_environment_route_policies where account_id = $1 and app_id = $2 and environment_slug = $3`,
		app.AccountID, app.ID, environment).Scan(&onlyDeclared, &routes)
	if err == nil {
		settings.OnlyAllowDeclaredRoutes = onlyDeclared
		if err := json.Unmarshal(routes, &settings.DeclaredRoutes); err != nil {
			return ProjectEnvironmentWorkloadSettings{}, ErrConflict
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentWorkloadSettings{}, mapErr(err)
	}
	return settings, nil
}
