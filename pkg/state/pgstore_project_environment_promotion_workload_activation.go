package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func promotionWorkloadActivationsTx(ctx context.Context, tx pgx.Tx, promotion ProjectEnvironmentPromotion, members []ProjectReleaseMember, rollback, completed bool) error {
	rows, err := tx.Query(ctx, promotionWorkloadSpecSelect+` where promotion_id = $1 order by app_id`, promotion.ID)
	if err != nil {
		return mapErr(err)
	}
	var captures []ProjectEnvironmentPromotionWorkloadSpec
	for rows.Next() {
		capture, err := scanPromotionWorkloadSpec(rows)
		if err != nil {
			rows.Close()
			return err
		}
		captures = append(captures, capture)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return mapErr(err)
	}
	byApp := make(map[string]string, len(members))
	for _, member := range members {
		byApp[member.AppID] = member.DeploymentID
	}
	for _, capture := range captures {
		app, err := scanApp(tx.QueryRow(ctx, `select `+appsSelectColumns+` from apps
			where id = $1 and account_id = $2 and project_id = $3 and status <> 'deleted' for update`,
			capture.AppID, promotion.AccountID, promotion.ProjectID))
		if err != nil {
			return err
		}
		prepared, err := scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
			join project_environment_workload_deployment_specs pin on pin.spec_id = s.id
			where s.id = $1 and s.app_id = $2 and pin.deployment_id = $3 and e.slug = $4`,
			capture.PreparedSpecID, app.ID, capture.DeploymentID, promotion.ToEnvironment))
		if err != nil {
			return err
		}
		if !rollback && byApp[app.ID] != capture.DeploymentID {
			return ErrConflict
		}
		current, settings, err := environmentWorkloadSettingsTx(ctx, tx, app, promotion.ToEnvironment)
		if err != nil {
			return err
		}
		expectedID, expectedHash := capture.PreviousSpecID, capture.PreviousHash
		if rollback || completed {
			expectedID, expectedHash = prepared.ID, prepared.Hash
		}
		if rollback && completed {
			expectedID, expectedHash = capture.PreviousSpecID, capture.PreviousHash
		}
		hash, err := WorkloadSettingsHash(settings)
		if err != nil || current.ID != expectedID || hash != expectedHash {
			return ErrConflict
		}
		if completed {
			continue
		}
		targetID, targetSettings := prepared.ID, prepared.Settings
		if rollback {
			targetID, targetSettings = capture.PreviousSpecID, capture.PreviousSettings
		}
		if targetID == "" {
			if _, err := tx.Exec(ctx, `delete from project_environment_workload_heads where environment_id = $1 and app_id = $2`, prepared.EnvironmentID, app.ID); err != nil {
				return mapErr(err)
			}
			if _, err := tx.Exec(ctx, `select pg_notify('app_changed', $1)`, app.ID); err != nil {
				return mapErr(err)
			}
		} else if _, err := tx.Exec(ctx, `insert into project_environment_workload_heads (environment_id, app_id, spec_id)
			values ($1, $2, $3) on conflict (environment_id, app_id) do update set spec_id = excluded.spec_id`, prepared.EnvironmentID, app.ID, targetID); err != nil {
			return mapErr(err)
		}
		if promotion.ToEnvironment == "production" {
			if err := replaceAppWorkloadSettingsTx(ctx, tx, app.ID, targetSettings); err != nil {
				return err
			}
		}
	}
	return nil
}

// Keep the legacy production projection coherent for app-wide consumers. The
// immutable deployment pin remains authoritative for each running revision.
func replaceAppWorkloadSettingsTx(ctx context.Context, tx pgx.Tx, appID string, settings ProjectEnvironmentWorkloadSettings) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return ErrInvalidArgument
	}
	var columns map[string]json.RawMessage
	if err := json.Unmarshal(raw, &columns); err != nil {
		return ErrInvalidArgument
	}
	columns["retry_policy"] = columns["retry_policy_json"]
	columns["only_declared_routes"] = columns["only_allow_declared_routes"]
	if settings.PublicAuthBasicSealed == nil {
		columns["public_auth_basic"] = json.RawMessage(`null`)
	} else {
		columns["public_auth_basic"], _ = json.Marshal("\\x" + hex.EncodeToString(settings.PublicAuthBasicSealed))
	}
	raw, err = json.Marshal(columns)
	if err != nil {
		return ErrInvalidArgument
	}
	parsedID, err := parsePgUUID(appID)
	if err != nil {
		return ErrInvalidArgument
	}
	err = new(sqlc.Queries).ReplaceProductionAppWorkloadSettings(ctx, tx, sqlc.ReplaceProductionAppWorkloadSettingsParams{
		AppID:    parsedID,
		Settings: raw,
	})
	return mapErr(err)
}

func syncProductionWorkloadSpecTx(ctx context.Context, tx pgx.Tx, app App, params UpdateAppParams) (App, error) {
	if app.ProjectID == "" {
		return app, nil
	}
	current, err := scanWorkloadSpec(tx.QueryRow(ctx, workloadSpecSelect+`
		join project_environment_workload_heads h on h.spec_id = s.id
		where e.account_id = $1 and e.project_id = $2 and e.slug = 'production' and s.app_id = $3`, app.AccountID, app.ProjectID, app.ID))
	if errors.Is(err, ErrNotFound) {
		return app, nil
	}
	if err != nil {
		return App{}, err
	}
	prior, err := current.Settings.ApplyTo(app)
	if err != nil {
		return App{}, err
	}
	settings, err := WorkloadSettingsFromApp(applyAppConfigurationParams(prior, params))
	if err != nil {
		return App{}, err
	}
	if _, err := putWorkloadSpecTx(ctx, tx, current.EnvironmentID, app.ID, current.Revision, settings); err != nil {
		return App{}, err
	}
	return settings.ApplyTo(app)
}
