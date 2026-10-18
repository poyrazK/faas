-- filename: 20261018094122957_app_runtime_generations.sql
-- ADR-1741: a per-app runtime generation, bumped in the writer's transaction
-- by every change a wake depends on, and announced on app_runtime_changed at
-- commit. schedd's owner cache keys its entries by it, and every write a wake
-- makes rechecks it, so a stale entry can never commit a decision.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_runtime_generations (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    generation bigint NOT NULL DEFAULT 1 CHECK (generation > 0),
    changed_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO app_runtime_generations (app_id) SELECT id FROM apps ON CONFLICT (app_id) DO NOTHING;

-- Bumps the app's generation and announces it. An app deleted in this
-- transaction has no row to bump; its deletion is announced by the apps
-- trigger.
CREATE OR REPLACE FUNCTION app_runtime_generation_bump(target uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE next bigint;
BEGIN
    IF target IS NULL THEN
        RETURN;
    END IF;
    UPDATE app_runtime_generations SET generation = generation + 1, changed_at = clock_timestamp()
    WHERE app_id = target RETURNING generation INTO next;
    IF NOT FOUND THEN
        INSERT INTO app_runtime_generations AS g (app_id)
        SELECT target WHERE EXISTS (SELECT 1 FROM apps WHERE id = target)
        ON CONFLICT (app_id) DO UPDATE SET generation = g.generation + 1, changed_at = clock_timestamp()
        RETURNING generation INTO next;
    END IF;
    IF next IS NOT NULL THEN
        PERFORM pg_notify('app_runtime_changed', json_build_object('app_id', target, 'generation', next)::text);
    END IF;
END;
$$;

-- Rows that name their app. TG_ARGV lists columns whose changes alone are
-- bookkeeping and do not bump the generation.
CREATE OR REPLACE FUNCTION app_runtime_generation_on_app_row() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND (to_jsonb(OLD) - TG_ARGV) = (to_jsonb(NEW) - TG_ARGV) THEN
        RETURN NULL;
    END IF;
    IF TG_OP <> 'INSERT' THEN
        PERFORM app_runtime_generation_bump(OLD.app_id);
    END IF;
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.app_id IS DISTINCT FROM OLD.app_id) THEN
        PERFORM app_runtime_generation_bump(NEW.app_id);
    END IF;
    RETURN NULL;
END;
$$;

-- Rows that name a deployment; the app is the deployment's.
CREATE OR REPLACE FUNCTION app_runtime_generation_on_deployment_row() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM app_runtime_generation_bump((SELECT d.app_id FROM deployments d WHERE d.id = OLD.deployment_id));
    END IF;
    IF TG_OP = 'INSERT' OR (TG_OP = 'UPDATE' AND NEW.deployment_id IS DISTINCT FROM OLD.deployment_id) THEN
        PERFORM app_runtime_generation_bump((SELECT d.app_id FROM deployments d WHERE d.id = NEW.deployment_id));
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION app_runtime_generation_on_apps() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO app_runtime_generations (app_id) VALUES (NEW.id) ON CONFLICT (app_id) DO NOTHING;
        PERFORM pg_notify('app_runtime_changed', json_build_object('app_id', NEW.id, 'generation', 1)::text);
    ELSIF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('app_runtime_changed', json_build_object('app_id', OLD.id, 'generation', 0)::text);
    ELSIF (to_jsonb(OLD) - TG_ARGV) <> (to_jsonb(NEW) - TG_ARGV) THEN
        PERFORM app_runtime_generation_bump(NEW.id);
    END IF;
    RETURN NULL;
END;
$$;

-- An account's status, plan and holds decide admission for every app it owns.
CREATE OR REPLACE FUNCTION app_runtime_generation_on_accounts() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owned uuid;
BEGIN
    IF OLD.status IS NOT DISTINCT FROM NEW.status AND OLD.plan IS NOT DISTINCT FROM NEW.plan
        AND OLD.abuse_hold_at IS NOT DISTINCT FROM NEW.abuse_hold_at
        AND OLD.overage_cap_cents IS NOT DISTINCT FROM NEW.overage_cap_cents
        AND OLD.egress_allowlist_extra IS NOT DISTINCT FROM NEW.egress_allowlist_extra
        AND OLD.deletion_requested_at IS NOT DISTINCT FROM NEW.deletion_requested_at THEN
        RETURN NULL;
    END IF;
    FOR owned IN SELECT id FROM apps WHERE account_id = NEW.id ORDER BY id LOOP
        PERFORM app_runtime_generation_bump(owned);
    END LOOP;
    RETURN NULL;
END;
$$;

-- A release set decides which deployment each member app serves.
CREATE OR REPLACE FUNCTION app_runtime_generation_on_release_sets() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE member uuid;
BEGIN
    FOR member IN SELECT DISTINCT m.app_id FROM project_release_members m
        WHERE m.release_id = COALESCE(NEW.id, OLD.id) ORDER BY m.app_id LOOP
        PERFORM app_runtime_generation_bump(member);
    END LOOP;
    RETURN NULL;
END;
$$;

-- The workload-retirement gate finds an app's retirements through its
-- environment's slug; renaming or removing an environment moves every app of
-- the project.
CREATE OR REPLACE FUNCTION app_runtime_generation_on_project_environments() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE member uuid;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.slug IS NOT DISTINCT FROM NEW.slug AND OLD.project_id IS NOT DISTINCT FROM NEW.project_id THEN
        RETURN NULL;
    END IF;
    FOR member IN SELECT id FROM apps WHERE project_id = OLD.project_id ORDER BY id LOOP
        PERFORM app_runtime_generation_bump(member);
    END LOOP;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS app_runtime_generation_project_environments ON project_environments;
CREATE TRIGGER app_runtime_generation_project_environments AFTER UPDATE OR DELETE ON project_environments
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_project_environments();

DROP TRIGGER IF EXISTS app_runtime_generation_apps ON apps;
CREATE TRIGGER app_runtime_generation_apps AFTER INSERT OR UPDATE OR DELETE ON apps
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_apps(
        'last_scale_out_at', 'last_scale_in_at', 'last_deploy_failed_email_at', 'purge_claimed_at', 'preview_destroy_commented_at');

DROP TRIGGER IF EXISTS app_runtime_generation_accounts ON accounts;
CREATE TRIGGER app_runtime_generation_accounts AFTER UPDATE ON accounts
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_accounts();

-- schedd alone writes the snapshot-miss backoff and the activity counters,
-- and keeps its cache current for its own writes. Every update renews
-- runtime_upgrade_routing_token (renew_runtime_upgrade_routing_token), so it
-- carries no change of its own.
DROP TRIGGER IF EXISTS app_runtime_generation_deployments ON deployments;
CREATE TRIGGER app_runtime_generation_deployments AFTER INSERT OR UPDATE OR DELETE ON deployments
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row(
        'snapshot_miss_count', 'snapshot_miss_last_at', 'snapshot_miss_backoff_until', 'first_wake_at',
        'first_5xx_window_ends_at', 'first_5xx_count', 'liveness_restart_count', 'canary_step_started_at',
        'runtime_upgrade_routing_token');

DROP TRIGGER IF EXISTS app_runtime_generation_release_sets ON project_release_sets;
CREATE TRIGGER app_runtime_generation_release_sets AFTER UPDATE OR DELETE ON project_release_sets
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_release_sets();

DROP TRIGGER IF EXISTS app_runtime_generation_release_members ON project_release_members;
CREATE TRIGGER app_runtime_generation_release_members AFTER INSERT OR UPDATE OR DELETE ON project_release_members
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();

DROP TRIGGER IF EXISTS app_runtime_generation_envs ON app_envs;
CREATE TRIGGER app_runtime_generation_envs AFTER INSERT OR UPDATE OR DELETE ON app_envs
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();

-- Delivery and reload bookkeeping is written by every boot of an app with
-- secrets; only the secret itself bumps the generation.
DROP TRIGGER IF EXISTS app_runtime_generation_secrets ON app_secrets;
CREATE TRIGGER app_runtime_generation_secrets AFTER INSERT OR UPDATE OR DELETE ON app_secrets
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row(
        'updated_at', 'delivered_version', 'delivery_status', 'last_delivery_attempt_at', 'last_delivered_at',
        'last_delivery_error_code', 'last_delivered_wake_id', 'last_delivered_instance_id',
        'last_runtime_reload_version', 'last_runtime_reload_revision', 'last_runtime_reload_projection',
        'last_runtime_reload_signal', 'last_runtime_reload_at', 'last_runtime_reload_error_code',
        'last_runtime_reload_instance_id');

DROP TRIGGER IF EXISTS app_runtime_generation_sidecar_layers ON deployment_sidecar_layers;
CREATE TRIGGER app_runtime_generation_sidecar_layers AFTER INSERT OR UPDATE OR DELETE ON deployment_sidecar_layers
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_deployment_row();

DROP TRIGGER IF EXISTS app_runtime_generation_workload_specs ON project_environment_workload_specs;
CREATE TRIGGER app_runtime_generation_workload_specs AFTER INSERT OR UPDATE OR DELETE ON project_environment_workload_specs
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();

DROP TRIGGER IF EXISTS app_runtime_generation_workload_heads ON project_environment_workload_heads;
CREATE TRIGGER app_runtime_generation_workload_heads AFTER INSERT OR UPDATE OR DELETE ON project_environment_workload_heads
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();

DROP TRIGGER IF EXISTS app_runtime_generation_workload_deployment_specs ON project_environment_workload_deployment_specs;
CREATE TRIGGER app_runtime_generation_workload_deployment_specs AFTER INSERT OR UPDATE OR DELETE ON project_environment_workload_deployment_specs
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_deployment_row();

DROP TRIGGER IF EXISTS app_runtime_generation_environment_owners ON deployment_runtime_environment_owners;
CREATE TRIGGER app_runtime_generation_environment_owners AFTER INSERT OR UPDATE OR DELETE ON deployment_runtime_environment_owners
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_deployment_row();

DROP TRIGGER IF EXISTS app_runtime_generation_config_changes ON app_runtime_config_changes;
CREATE TRIGGER app_runtime_generation_config_changes AFTER INSERT OR UPDATE OR DELETE ON app_runtime_config_changes
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();

DROP TRIGGER IF EXISTS app_runtime_generation_config_scope_changes ON app_runtime_config_scope_changes;
CREATE TRIGGER app_runtime_generation_config_scope_changes AFTER INSERT OR UPDATE OR DELETE ON app_runtime_config_scope_changes
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();

-- Leases, checkpoints and observations are the enrollment worker's
-- bookkeeping; the runtime rule reads none of them.
DROP TRIGGER IF EXISTS app_runtime_generation_application_standards ON app_application_standards;
CREATE TRIGGER app_runtime_generation_application_standards AFTER INSERT OR UPDATE OR DELETE ON app_application_standards
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row(
        'updated_at', 'lease_owner', 'lease_generation', 'lease_until', 'observed_revision',
        'observation_checked_at', 'observation_revision');

DROP TRIGGER IF EXISTS app_runtime_generation_workload_retirements ON environment_workload_retirements;
CREATE TRIGGER app_runtime_generation_workload_retirements AFTER INSERT OR UPDATE OR DELETE ON environment_workload_retirements
    FOR EACH ROW EXECUTE FUNCTION app_runtime_generation_on_app_row();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_runtime_generation_workload_retirements ON environment_workload_retirements;
DROP TRIGGER IF EXISTS app_runtime_generation_application_standards ON app_application_standards;
DROP TRIGGER IF EXISTS app_runtime_generation_config_scope_changes ON app_runtime_config_scope_changes;
DROP TRIGGER IF EXISTS app_runtime_generation_config_changes ON app_runtime_config_changes;
DROP TRIGGER IF EXISTS app_runtime_generation_environment_owners ON deployment_runtime_environment_owners;
DROP TRIGGER IF EXISTS app_runtime_generation_workload_deployment_specs ON project_environment_workload_deployment_specs;
DROP TRIGGER IF EXISTS app_runtime_generation_workload_heads ON project_environment_workload_heads;
DROP TRIGGER IF EXISTS app_runtime_generation_workload_specs ON project_environment_workload_specs;
DROP TRIGGER IF EXISTS app_runtime_generation_sidecar_layers ON deployment_sidecar_layers;
DROP TRIGGER IF EXISTS app_runtime_generation_secrets ON app_secrets;
DROP TRIGGER IF EXISTS app_runtime_generation_envs ON app_envs;
DROP TRIGGER IF EXISTS app_runtime_generation_release_members ON project_release_members;
DROP TRIGGER IF EXISTS app_runtime_generation_release_sets ON project_release_sets;
DROP TRIGGER IF EXISTS app_runtime_generation_deployments ON deployments;
DROP TRIGGER IF EXISTS app_runtime_generation_accounts ON accounts;
DROP TRIGGER IF EXISTS app_runtime_generation_apps ON apps;
DROP TRIGGER IF EXISTS app_runtime_generation_project_environments ON project_environments;
DROP FUNCTION IF EXISTS app_runtime_generation_on_project_environments();
DROP FUNCTION IF EXISTS app_runtime_generation_on_release_sets();
DROP FUNCTION IF EXISTS app_runtime_generation_on_accounts();
DROP FUNCTION IF EXISTS app_runtime_generation_on_apps();
DROP FUNCTION IF EXISTS app_runtime_generation_on_deployment_row();
DROP FUNCTION IF EXISTS app_runtime_generation_on_app_row();
DROP FUNCTION IF EXISTS app_runtime_generation_bump(uuid);
DROP TABLE IF EXISTS app_runtime_generations;
-- +goose StatementEnd
