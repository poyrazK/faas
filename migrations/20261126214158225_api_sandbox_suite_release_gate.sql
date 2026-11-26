-- filename: 20261126214158225_api_sandbox_suite_release_gate.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE app_release_approval_policies
    ADD COLUMN IF NOT EXISTS require_passing_sandbox_suite boolean NOT NULL DEFAULT false;

CREATE TABLE IF NOT EXISTS api_sandbox_release_admissions (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    task_id uuid NOT NULL UNIQUE REFERENCES app_tasks(id) ON DELETE CASCADE,
    suite_run_id uuid NOT NULL
);

-- The receipt and release task are committed together. Keeping this check
-- in Postgres also fences release-command writers from a previous version.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION require_api_sandbox_release_admission() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    policy app_release_approval_policies%ROWTYPE;
BEGIN
    SELECT * INTO policy FROM app_release_approval_policies WHERE app_id = NEW.app_id FOR SHARE;
    IF NOT FOUND OR NOT policy.require_passing_sandbox_suite THEN
        RETURN NULL;
    END IF;

    -- Match the existing pre-command approval boundary. A first production
    -- release has no serving baseline and other scopes have their own lane.
    IF EXISTS (
        SELECT 1 FROM deployments d JOIN apps a ON a.id = d.app_id
        WHERE d.id = NEW.deployment_id AND d.app_id = NEW.app_id
          AND d.status = 'snapshotting' AND cardinality(d.release_command) > 0
          AND coalesce(nullif(d.scope, ''), 'default') IN ('default', 'production')
          AND coalesce(a.manifest->>'execution_mode', '') IN ('', 'request')
          AND NOT (d.environment_workload_runtime IS NOT NULL AND d.environment_workload_held)
          AND d.rollout_completed_at IS NULL
          AND (d.created_at >= policy.created_at OR d.rollout_started_at >= policy.created_at)
          AND EXISTS (
              SELECT 1 FROM deployments live WHERE live.app_id = d.app_id AND live.status = 'live'
                AND live.scope IN ('default', 'production')
          )
    ) AND NOT EXISTS (
        SELECT 1 FROM api_sandbox_release_admissions
        WHERE deployment_id = NEW.deployment_id AND task_id = NEW.id
    ) THEN
        RAISE EXCEPTION 'production release command requires a sandbox suite admission'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'api_sandbox_release_command_requires_admission';
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'app_tasks'::regclass AND tgname = 'api_sandbox_release_command_requires_admission'
    ) THEN
        CREATE CONSTRAINT TRIGGER api_sandbox_release_command_requires_admission
        AFTER INSERT ON app_tasks DEFERRABLE INITIALLY DEFERRED
        FOR EACH ROW WHEN (NEW.kind = 'release')
        EXECUTE FUNCTION require_api_sandbox_release_admission();
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER api_sandbox_release_command_requires_admission ON app_tasks;
DROP FUNCTION require_api_sandbox_release_admission();
DROP TABLE api_sandbox_release_admissions;
ALTER TABLE app_release_approval_policies DROP COLUMN require_passing_sandbox_suite;
