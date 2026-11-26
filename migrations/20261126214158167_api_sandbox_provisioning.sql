-- filename: 20261126214158167_api_sandbox_provisioning.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_sandbox_provisioning (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    smoke_path text NOT NULL DEFAULT '' CHECK (octet_length(smoke_path) <= 512),
    phase text NOT NULL DEFAULT 'preparing_database'
        CHECK (phase IN ('preparing_database', 'running_migrations', 'starting_release', 'verifying_api', 'live', 'failed')),
    started_at timestamptz NOT NULL DEFAULT now(),
    next_attempt_at timestamptz DEFAULT now(),
    lease_token uuid,
    lease_until timestamptz,
    deployment_id uuid,
    failure jsonb,
    verification jsonb,
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK ((phase IN ('live', 'failed')) = (next_attempt_at IS NULL)),
    CHECK ((phase = 'live') = (verification IS NOT NULL)),
    CHECK (verification IS NULL OR (
        deployment_id IS NOT NULL AND jsonb_typeof(verification) = 'object'
        AND (verification->>'deployment_id') IS NOT DISTINCT FROM deployment_id::text
    ))
);

CREATE INDEX IF NOT EXISTS api_sandbox_provisioning_pending_idx
    ON api_sandbox_provisioning (next_attempt_at, app_id)
    WHERE next_attempt_at IS NOT NULL;

INSERT INTO api_sandbox_provisioning (app_id)
SELECT id FROM apps
WHERE status = 'active' AND preview_pr_state = 'open' AND preview_expires_at > now()
  AND starts_with(preview_of_slug, 'api-sandbox:')
  AND manifest->>'api_sandbox_source_deployment_id' <> ''
ON CONFLICT DO NOTHING;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'apps'::regclass AND conname = 'api_sandbox_fixed_egress'
    ) THEN
        ALTER TABLE apps ADD CONSTRAINT api_sandbox_fixed_egress
            CHECK (coalesce(preview_of_slug, '') NOT LIKE 'api-sandbox:%'
                OR (egress_allowlist = ARRAY['127.0.0.1/32'::cidr] AND cardinality(egress_ports) = 0))
            NOT VALID;
    END IF;
END;
$$;
-- +goose StatementEnd

-- The sandbox's generated app ID also reserves its one database. Guard both
-- ends so the isolated database and its temporary credentials stay together.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_api_sandbox_postgres_binding() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM apps
        WHERE id = NEW.app_id AND starts_with(preview_of_slug, 'api-sandbox:')
          AND NOT (
              NEW.database_id = NEW.app_id AND NEW.scope = 'default'
              AND ((NEW.environment_key = 'DATABASE_URL' AND NEW.access = 'read_write')
                  OR (NEW.environment_key = 'MIGRATION_DATABASE_URL' AND NEW.access = 'migration'))
          )
    ) OR EXISTS (
        SELECT 1 FROM apps
        WHERE id = NEW.database_id AND id <> NEW.app_id AND starts_with(preview_of_slug, 'api-sandbox:')
    ) THEN
        RAISE EXCEPTION 'API sandbox database bindings are dedicated to the sandbox'
            USING ERRCODE = 'check_violation', CONSTRAINT = 'managed_postgres_sandbox_binding';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgrelid = 'managed_postgres_bindings'::regclass
          AND tgname = 'managed_postgres_sandbox_binding_guard'
    ) THEN
        CREATE TRIGGER managed_postgres_sandbox_binding_guard
        BEFORE INSERT OR UPDATE OF app_id, database_id, scope, environment_key, access ON managed_postgres_bindings
        FOR EACH ROW EXECUTE FUNCTION guard_api_sandbox_postgres_binding();
    END IF;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER managed_postgres_sandbox_binding_guard ON managed_postgres_bindings;
DROP FUNCTION guard_api_sandbox_postgres_binding();
ALTER TABLE apps DROP CONSTRAINT api_sandbox_fixed_egress;
DROP TABLE IF EXISTS api_sandbox_provisioning;
