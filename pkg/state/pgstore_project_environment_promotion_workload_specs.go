package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PgStore) CreateDeploymentForEnvironmentPromotion(ctx context.Context, deployment Deployment, input ProjectEnvironmentPromotionWorkloadSpecInput) (Deployment, error) {
	created, _, err := s.createDeployment(ctx, deployment, nil, &input)
	return created, err
}

const promotionWorkloadSpecSelect = `select promotion_id::text, app_id::text, deployment_id::text,
	prepared_spec_id::text, coalesce(previous_spec_id::text, ''), source_hash, previous_hash, previous_settings
	from project_environment_promotion_workload_specs`

func scanPromotionWorkloadSpec(row pgx.Row) (ProjectEnvironmentPromotionWorkloadSpec, error) {
	var capture ProjectEnvironmentPromotionWorkloadSpec
	var raw []byte
	if err := row.Scan(&capture.PromotionID, &capture.AppID, &capture.DeploymentID, &capture.PreparedSpecID,
		&capture.PreviousSpecID, &capture.SourceHash, &capture.PreviousHash, &raw); err != nil {
		return ProjectEnvironmentPromotionWorkloadSpec{}, mapErr(err)
	}
	if err := json.Unmarshal(raw, &capture.PreviousSettings); err != nil {
		return ProjectEnvironmentPromotionWorkloadSpec{}, ErrConflict
	}
	hash, err := WorkloadSettingsHash(capture.PreviousSettings)
	if err != nil || hash != capture.PreviousHash {
		return ProjectEnvironmentPromotionWorkloadSpec{}, ErrConflict
	}
	return capture, nil
}

func (s *PgStore) ProjectEnvironmentPromotionWorkloadSpec(ctx context.Context, accountID, promotionID, appID string) (ProjectEnvironmentPromotionWorkloadSpec, error) {
	return scanPromotionWorkloadSpec(s.pool.QueryRow(ctx, promotionWorkloadSpecSelect+`
		where promotion_id = $1 and app_id = $2 and exists
		(select 1 from project_environment_promotions p where p.id = $1 and p.account_id = $3)`, promotionID, appID, accountID))
}

func lockPromotionWorkloadEnvironmentsTx(ctx context.Context, tx pgx.Tx, promotionID string) error {
	rows, err := tx.Query(ctx, `select e.id from project_environments e
		join project_environment_promotions p on p.project_id = e.project_id and p.account_id = e.account_id
		where p.id = $1 and e.slug in (p.from_environment, p.to_environment)
		order by e.id for update of e`, promotionID)
	if err != nil {
		return mapErr(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return mapErr(err)
	}
	if count != 2 {
		return ErrConflict
	}
	return nil
}

func environmentWorkloadSettingsTx(ctx context.Context, tx pgx.Tx, app App, environment string) (ProjectEnvironmentWorkloadSpec, ProjectEnvironmentWorkloadSettings, error) {
	spec, err := workloadSpecOrZero(scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
		join project_environment_workload_heads h on h.spec_id = s.id
		where e.account_id = $1 and e.project_id = $2 and e.slug = $3 and s.app_id = $4`, app.AccountID, app.ProjectID, environment, app.ID)))
	if err != nil {
		return ProjectEnvironmentWorkloadSpec{}, ProjectEnvironmentWorkloadSettings{}, err
	}
	if spec.ID != "" {
		return spec, spec.Settings, nil
	}
	settings, err := materializeWorkloadSettingsTx(ctx, tx, app, environment)
	return spec, settings, err
}

func preparePromotionWorkloadSpecTx(ctx context.Context, tx pgx.Tx, deployment Deployment, input ProjectEnvironmentPromotionWorkloadSpecInput) error {
	app, err := scanApp(tx.QueryRow(ctx, `select `+appsSelectColumns+` from apps where id = $1`, deployment.AppID))
	if err != nil {
		return err
	}
	promotion, err := scanProjectEnvironmentPromotion(tx.QueryRow(ctx, `select `+projectEnvironmentPromotionSelectColumns+`
		from project_environment_promotions where id = $1 and account_id = $2 and project_id = $3`, input.PromotionID, app.AccountID, app.ProjectID))
	if err != nil {
		return err
	}
	if !promotion.SyncConfig || !promotion.ReleaseGraphMode || promotion.Status != "running" ||
		promotion.ToEnvironment != deployment.Scope || promotion.TargetReleaseSetID != "" {
		return ErrConflict
	}
	var found int
	if err := tx.QueryRow(ctx, `select 1 from project_environment_promotion_workloads w
		join deployments d on d.id::text = w.source_deployment_id
		where w.promotion_id = $1 and w.workload_slug = $2 and w.source_deployment_id = $3
		and d.app_id = $4 and d.scope = $5 and d.status = 'live'`, promotion.ID, app.Slug,
		input.SourceDeploymentID, app.ID, promotion.FromEnvironment).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return mapErr(err)
	}
	previous, targetSettings, err := environmentWorkloadSettingsTx(ctx, tx, app, promotion.ToEnvironment)
	if err != nil {
		return err
	}
	sourceSettings, err := materializeWorkloadSettingsTx(ctx, tx, app, promotion.FromEnvironment)
	if err != nil {
		return err
	}
	pinned, err := workloadSpecOrZero(scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
		join project_environment_workload_deployment_specs pin on pin.spec_id = s.id
		where pin.deployment_id = $1`, input.SourceDeploymentID)))
	if err != nil {
		return err
	}
	if pinned.ID != "" {
		if pinned.AppID != app.ID || pinned.EnvironmentSlug != promotion.FromEnvironment {
			return ErrConflict
		}
		sourceSettings = pinned.Settings
	}
	sourceHash, err := WorkloadSettingsHash(sourceSettings)
	if err != nil || sourceHash != input.SourceHash {
		return ErrConflict
	}
	targetHash, err := WorkloadSettingsHash(targetSettings)
	if err != nil || targetHash != input.PreviousTargetHash {
		return ErrConflict
	}
	settings, err := WorkloadSettingsForPromotion(sourceSettings, targetSettings)
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
	previousRaw, err := json.Marshal(targetSettings)
	if err != nil {
		return ErrInvalidArgument
	}
	var environmentID string
	if err := tx.QueryRow(ctx, `select id::text from project_environments where account_id = $1 and project_id = $2 and slug = $3`,
		app.AccountID, app.ProjectID, deployment.Scope).Scan(&environmentID); err != nil {
		return mapErr(err)
	}
	var revision int64
	if err := tx.QueryRow(ctx, `select coalesce(max(revision), 0) + 1 from project_environment_workload_specs
		where environment_id = $1 and app_id = $2`, environmentID, app.ID).Scan(&revision); err != nil {
		return mapErr(err)
	}
	var previousDeploymentID string
	if err := tx.QueryRow(ctx, `select previous_target_deployment_id from project_environment_promotion_workloads
		where promotion_id = $1 and workload_slug = $2`, promotion.ID, app.Slug).Scan(&previousDeploymentID); err != nil {
		return mapErr(err)
	}
	if previousDeploymentID != "" {
		var alreadyPinned bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from project_environment_workload_deployment_specs where deployment_id = $1)`, previousDeploymentID).Scan(&alreadyPinned); err != nil {
			return mapErr(err)
		}
		if !alreadyPinned {
			legacy, err := materializeWorkloadSettingsTx(ctx, tx, app, promotion.ToEnvironment)
			if err != nil {
				return err
			}
			legacyHash, err := WorkloadSettingsHash(legacy)
			if err != nil {
				return err
			}
			legacyRaw, err := json.Marshal(legacy)
			if err != nil {
				return ErrInvalidArgument
			}
			legacyID := uuid.NewString()
			if _, err := tx.Exec(ctx, `insert into project_environment_workload_specs
				(id, environment_id, app_id, revision, config_hash, settings) values ($1, $2, $3, $4, $5, $6)`, legacyID, environmentID, app.ID, revision, legacyHash, legacyRaw); err != nil {
				return mapErr(err)
			}
			if _, err := tx.Exec(ctx, `insert into project_environment_workload_deployment_specs (deployment_id, spec_id)
				select d.id, $1::uuid from deployments d where d.id = $2 and d.app_id = $3
				and (d.scope = $4 or ($4 = 'production' and d.scope = 'default'))`, legacyID, previousDeploymentID, app.ID, promotion.ToEnvironment); err != nil {
				return mapErr(err)
			}
			revision++
		}
	}
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `insert into project_environment_workload_specs
		(id, environment_id, app_id, revision, config_hash, settings) values ($1, $2, $3, $4, $5, $6)`, id, environmentID, app.ID, revision, hash, raw); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `insert into project_environment_workload_deployment_specs (deployment_id, spec_id) values ($1, $2)`, deployment.ID, id); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `insert into project_environment_promotion_workload_specs
		(promotion_id, app_id, deployment_id, prepared_spec_id, previous_spec_id, source_hash, previous_hash, previous_settings)
		values ($1, $2, $3, $4, nullif($5, '')::uuid, $6, $7, $8)`, promotion.ID, app.ID, deployment.ID,
		id, previous.ID, sourceHash, targetHash, previousRaw); err != nil {
		return mapErr(err)
	}
	return nil
}
