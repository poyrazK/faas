package state

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
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
	_, err = tx.Exec(ctx, `with config as (select (json_populate_record(null::apps, $2::json)).*)
		update apps a set visibility = c.visibility, type = c.type, runtime = nullif(c.runtime, ''),
		ram_mb = c.ram_mb, cpu_millicores = nullif(c.cpu_millicores, 0), idle_timeout_s = nullif(c.idle_timeout_s, 0),
		max_concurrency = c.max_concurrency, request_rate_limit_rps = c.request_rate_limit_rps,
		request_rate_limit_burst = c.request_rate_limit_burst, min_instances = c.min_instances,
		egress_allowlist = coalesce(c.egress_allowlist, '{}'), egress_ports = coalesce(c.egress_ports, '{}'), static_egress_ip = c.static_egress_ip,
		public_auth_ip_allowlist = coalesce(c.public_auth_ip_allowlist, '{}'), autoscale_target_rps = c.autoscale_target_rps,
		autoscale_target_cpu_pct = c.autoscale_target_cpu_pct, root_dir = c.root_dir, workload_class = c.workload_class,
		streaming_enabled = c.streaming_enabled, websocket_enabled = c.websocket_enabled,
		route_metrics_enabled = c.route_metrics_enabled, app_protocol = c.app_protocol, maintenance_mode = c.maintenance_mode,
		only_declared_routes = c.only_declared_routes, declared_routes = coalesce(c.declared_routes, '[]'::jsonb),
		require_signed = c.require_signed, security_policy = c.security_policy, start_command = nullif(c.start_command, ''),
		manifest = coalesce(c.manifest, '{}'::jsonb), scaling_policy = coalesce(c.scaling_policy, '{}'::jsonb),
		retry_policy = coalesce(c.retry_policy, '{}'::jsonb), overflow_node = c.overflow_node,
		warm_snapshot_enabled = c.warm_snapshot_enabled, require_authn = c.require_authn,
		public_auth_mode = c.public_auth_mode, consumer_auth_mode = c.consumer_auth_mode,
		public_auth_basic = c.public_auth_basic, warm_snapshot_min_requests = c.warm_snapshot_min_requests,
		warm_snapshot_min_ms = c.warm_snapshot_min_ms, warm_pool_size = c.warm_pool_size,
		eviction_priority = c.eviction_priority, cors_default_enabled = c.cors_default_enabled,
		cors_default_origins = coalesce(c.cors_default_origins, '{}'), scaling_policy_revision = a.scaling_policy_revision + 1
		from config c where a.id = $1`, appID, raw)
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
