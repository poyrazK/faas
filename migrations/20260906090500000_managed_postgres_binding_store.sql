-- filename: 20260906090500000_managed_postgres_binding_store.sql

-- +goose Up
-- +goose StatementBegin
-- Binding reconciliation uses the same persisted lease and retry model as
-- database reconciliation. Access is a Gregale contract; adapters translate
-- it to a provider-specific role without leaking provider vocabulary here.
ALTER TABLE managed_postgres_bindings
    ADD COLUMN IF NOT EXISTS access text NOT NULL DEFAULT 'read_write'
        CHECK (access IN ('read_write', 'read_only')),
    ADD COLUMN IF NOT EXISTS last_error_code text
        CHECK (last_error_code ~ '^[a-z][a-z0-9_]{0,62}$'),
    ADD COLUMN IF NOT EXISTS lease_token text,
    ADD COLUMN IF NOT EXISTS lease_until timestamptz,
    ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 0
        CHECK (attempt_count BETWEEN 0 AND 30),
    ADD COLUMN IF NOT EXISTS retry_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT managed_postgres_bindings_lease_pair_chk
        CHECK ((lease_token IS NULL) = (lease_until IS NULL));

-- A runtime environment key belongs to one active binding, irrespective of
-- which database it targets. The former index included database_id and could
-- therefore admit two databases that both wanted to own DATABASE_URL.
DROP INDEX IF EXISTS managed_postgres_bindings_target_idx;
CREATE UNIQUE INDEX managed_postgres_bindings_target_idx
    ON managed_postgres_bindings(app_id, scope, environment_key)
    WHERE state <> 'deleted';

CREATE INDEX managed_postgres_bindings_reconcile_idx
    ON managed_postgres_bindings(retry_at, id)
    WHERE state IN ('provisioning', 'failed', 'deleting');

-- Managed credentials remain ordinary encrypted app-secret rows at runtime.
-- These columns record who owns the row; no provider URL or password is ever
-- stored in the managed-postgres catalog.
ALTER TABLE app_secrets
    ADD COLUMN IF NOT EXISTS managed_postgres_binding_id uuid
        REFERENCES managed_postgres_bindings(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS managed_credential_ref text,
    ADD COLUMN IF NOT EXISTS managed_credential_generation bigint,
    ADD CONSTRAINT app_secrets_managed_postgres_owner_chk CHECK (
        (managed_postgres_binding_id IS NULL
            AND managed_credential_ref IS NULL
            AND managed_credential_generation IS NULL)
        OR
        (managed_postgres_binding_id IS NOT NULL
            AND managed_credential_ref IS NOT NULL
            AND managed_credential_generation >= 1)
    );

CREATE UNIQUE INDEX app_secrets_managed_postgres_binding_idx
    ON app_secrets(managed_postgres_binding_id)
    WHERE managed_postgres_binding_id IS NOT NULL;
CREATE UNIQUE INDEX app_secrets_managed_credential_ref_idx
    ON app_secrets(managed_credential_ref)
    WHERE managed_credential_ref IS NOT NULL;

-- Both the binding catalog and customer secret writers take this transaction
-- lock before inspecting either table. hashtextextended collisions only cause
-- harmless extra serialization; they cannot weaken exclusivity.
CREATE OR REPLACE FUNCTION managed_secret_target_lock_key(
    target_app_id uuid,
    target_scope text,
    target_key text
) RETURNS bigint LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    SELECT hashtextextended(
        target_app_id::text || chr(31) || target_scope || chr(31) || target_key,
        0
    );
$$;

CREATE OR REPLACE FUNCTION guard_managed_postgres_secret_owner()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.managed_postgres_binding_id IS NOT NULL AND NOT EXISTS (
        SELECT 1
        FROM managed_postgres_bindings binding
        WHERE binding.id = NEW.managed_postgres_binding_id
          AND binding.account_id = NEW.account_id
          AND binding.app_id = NEW.app_id
          AND binding.scope = NEW.scope
          AND binding.environment_key = NEW.key
          AND binding.credential_generation = NEW.managed_credential_generation
          AND binding.state <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'managed app secret owner does not match its binding'
            USING ERRCODE = '23514', CONSTRAINT = 'app_secret_managed_postgres_owner';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS app_secret_managed_postgres_owner_guard ON app_secrets;
CREATE TRIGGER app_secret_managed_postgres_owner_guard
    BEFORE INSERT OR UPDATE OF account_id,app_id,scope,key,
        managed_postgres_binding_id,managed_credential_generation
    ON app_secrets
    FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_secret_owner();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_secret_managed_postgres_owner_guard ON app_secrets;
DROP FUNCTION IF EXISTS guard_managed_postgres_secret_owner();
DROP FUNCTION IF EXISTS managed_secret_target_lock_key(uuid, text, text);
DROP INDEX IF EXISTS app_secrets_managed_credential_ref_idx;
DROP INDEX IF EXISTS app_secrets_managed_postgres_binding_idx;
ALTER TABLE app_secrets
    DROP CONSTRAINT IF EXISTS app_secrets_managed_postgres_owner_chk,
    DROP COLUMN IF EXISTS managed_credential_generation,
    DROP COLUMN IF EXISTS managed_credential_ref,
    DROP COLUMN IF EXISTS managed_postgres_binding_id;
DROP INDEX IF EXISTS managed_postgres_bindings_reconcile_idx;
DROP INDEX IF EXISTS managed_postgres_bindings_target_idx;
CREATE UNIQUE INDEX managed_postgres_bindings_target_idx
    ON managed_postgres_bindings(database_id, app_id, scope, environment_key)
    WHERE state <> 'deleted';
ALTER TABLE managed_postgres_bindings
    DROP CONSTRAINT IF EXISTS managed_postgres_bindings_lease_pair_chk,
    DROP COLUMN IF EXISTS retry_at,
    DROP COLUMN IF EXISTS attempt_count,
    DROP COLUMN IF EXISTS lease_until,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS last_error_code,
    DROP COLUMN IF EXISTS access;
-- +goose StatementEnd
