-- +goose Up
-- +goose StatementBegin

-- A deployment may now have independent main and sidecar reload contracts.
-- Keep sidecar image opt-in metadata separate from customer-controlled
-- deployment JSON so the runtime roster can report capability before the
-- sidecar has emitted its first live-reload observation.
CREATE TABLE IF NOT EXISTS deployment_sidecar_secret_reload_signals (
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    sidecar_name text NOT NULL,
    signal text NOT NULL,
    PRIMARY KEY (deployment_id, sidecar_name),
    CONSTRAINT deployment_sidecar_secret_reload_name_chk
        CHECK (sidecar_name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT deployment_sidecar_secret_reload_signal_chk
        CHECK (signal IN ('', 'SIGHUP', 'SIGUSR1', 'SIGUSR2'))
);

ALTER TABLE app_secret_runtime_reload_observations
    ADD COLUMN IF NOT EXISTS workload_name text NOT NULL DEFAULT '';

ALTER TABLE app_secret_runtime_reload_observations
    DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_observations_pkey;

ALTER TABLE app_secret_runtime_reload_observations
    ADD CONSTRAINT app_secret_runtime_reload_observations_pkey
        PRIMARY KEY (app_id, scope, key, instance_id, workload_name);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
         WHERE conname = 'app_secret_runtime_reload_observation_workload_name_chk'
           AND conrelid = 'app_secret_runtime_reload_observations'::regclass
    ) THEN
        ALTER TABLE app_secret_runtime_reload_observations
            ADD CONSTRAINT app_secret_runtime_reload_observation_workload_name_chk
                CHECK (workload_name = '' OR workload_name ~ '^[a-z0-9][a-z0-9-]{0,62}$');
    END IF;
END$$;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE app_secret_runtime_reload_observations
    DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_observation_workload_name_chk,
    DROP CONSTRAINT IF EXISTS app_secret_runtime_reload_observations_pkey;

-- The old key cannot represent multiple workload observations for one
-- runtime+secret. Discard sidecar-only rows before restoring that key shape.
DELETE FROM app_secret_runtime_reload_observations WHERE workload_name <> '';

ALTER TABLE app_secret_runtime_reload_observations
    DROP COLUMN IF EXISTS workload_name;

ALTER TABLE app_secret_runtime_reload_observations
    ADD CONSTRAINT app_secret_runtime_reload_observations_pkey
        PRIMARY KEY (app_id, scope, key, instance_id);

DROP TABLE IF EXISTS deployment_sidecar_secret_reload_signals;
-- +goose StatementEnd
