package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The caller holds the source environment lock; app locks also fence legacy
// UpdateApp and deployment creation while materializing configuration.
func copyProjectEnvironmentWorkloadSpecs(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone, target ProjectEnvironment) error {
	rows, err := tx.Query(ctx, `select `+appsSelectColumns+` from apps
  where account_id = $1 and project_id = $2 and status <> 'deleted' and preview_of_slug is null
  order by id for update`, clone.AccountID, clone.ProjectID)
	if err != nil {
		return mapErr(err)
	}
	apps, err := scanApps(rows)
	rows.Close()
	if err != nil {
		return mapErr(err)
	}
	for _, app := range apps {
		source, err := scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
   join project_environment_workload_heads h on h.spec_id = s.id
   where e.account_id = $1 and e.project_id = $2 and e.slug = $3 and s.app_id = $4`,
			clone.AccountID, clone.ProjectID, clone.SourceSlug, app.ID))
		settings := source.Settings
		if errors.Is(err, ErrNotFound) {
			settings, err = WorkloadSettingsFromApp(app)
			if err != nil {
				return err
			}
			var routes []byte
			var onlyDeclared bool
			routeErr := tx.QueryRow(ctx, `select only_allow_declared_routes, declared_routes
				from project_environment_route_policies where account_id = $1 and app_id = $2 and environment_slug = $3`,
				clone.AccountID, app.ID, clone.SourceSlug).Scan(&onlyDeclared, &routes)
			if routeErr == nil {
				settings.OnlyAllowDeclaredRoutes = onlyDeclared
				if err := json.Unmarshal(routes, &settings.DeclaredRoutes); err != nil {
					return ErrConflict
				}
			} else if !errors.Is(routeErr, pgx.ErrNoRows) {
				return mapErr(routeErr)
			}
		}
		if err != nil {
			return err
		}
		hash, err := WorkloadSettingsHash(settings)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			return ErrInvalidArgument
		}
		id := uuid.NewString()
		if _, err := tx.Exec(ctx, `insert into project_environment_workload_specs
   (id, environment_id, app_id, revision, config_hash, settings)
   values ($1, $2, $3, 1, $4, $5)`, id, target.ID, app.ID, hash, raw); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `insert into project_environment_workload_heads
   (environment_id, app_id, spec_id) values ($1, $2, $3)`, target.ID, app.ID, id); err != nil {
			return mapErr(err)
		}
	}
	return nil
}
