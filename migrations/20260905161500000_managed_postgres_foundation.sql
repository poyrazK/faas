-- filename: 20260905161500000_managed_postgres_foundation.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_databases (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    region text NOT NULL CHECK (region ~ '^[a-z][a-z0-9-]{0,62}$'),
    postgres_major smallint NOT NULL CHECK (postgres_major BETWEEN 12 AND 99),
    service_class text NOT NULL CHECK (service_class IN ('development','burstable','production')),
    availability text NOT NULL CHECK (availability IN ('single_zone','high_availability')),
    scale_to_zero boolean NOT NULL DEFAULT false,
    storage_limit_bytes bigint NOT NULL DEFAULT 0 CHECK (storage_limit_bytes >= 0),
    restore_window_seconds bigint NOT NULL DEFAULT 0 CHECK (restore_window_seconds >= 0),
    backend_id text NOT NULL CHECK (backend_id ~ '^[a-z][a-z0-9-]{0,62}$'),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    provider_resource_id text,
    state text NOT NULL DEFAULT 'provisioning'
        CHECK (state IN ('provisioning','ready','updating','deleting','failed','deleted')),
    desired_generation bigint NOT NULL DEFAULT 1 CHECK (desired_generation >= 1),
    observed_generation bigint NOT NULL DEFAULT 0
        CHECK (observed_generation >= 0 AND observed_generation <= desired_generation),
    last_error_code text CHECK (last_error_code ~ '^[a-z][a-z0-9_]{0,62}$'),
    lease_token text,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CHECK (state <> 'ready' OR (provider_resource_id IS NOT NULL AND observed_generation = desired_generation)),
    CHECK ((state = 'deleted') = (deleted_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_databases_name_idx
    ON managed_postgres_databases(account_id,name) WHERE state <> 'deleted';
CREATE INDEX IF NOT EXISTS managed_postgres_databases_account_state_idx
    ON managed_postgres_databases(account_id,state);
CREATE INDEX IF NOT EXISTS managed_postgres_databases_backend_idx
    ON managed_postgres_databases(backend_id) WHERE state <> 'deleted';

-- Bindings are deliberately separate from database ownership. A future API
-- can bind one account database to multiple apps/scopes while keeping each
-- app credential independently revocable. credential_ref is an opaque handle
-- into the encrypted app-secret subsystem, never a URL or password.
CREATE TABLE IF NOT EXISTS managed_postgres_bindings (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id),
    database_id uuid NOT NULL REFERENCES managed_postgres_databases(id),
    app_id uuid NOT NULL REFERENCES apps(id),
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 63),
    environment_key text NOT NULL DEFAULT 'DATABASE_URL'
        CHECK (environment_key ~ '^[A-Z_][A-Z0-9_]{0,126}$'),
    provider_identity_id text,
    credential_ref text,
    credential_generation bigint NOT NULL DEFAULT 1 CHECK (credential_generation >= 1),
    state text NOT NULL DEFAULT 'provisioning'
        CHECK (state IN ('provisioning','ready','deleting','failed','deleted')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    CHECK (state <> 'ready' OR (provider_identity_id IS NOT NULL AND credential_ref IS NOT NULL)),
    CHECK ((state = 'deleted') = (deleted_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_bindings_target_idx
    ON managed_postgres_bindings(database_id,app_id,scope,environment_key) WHERE state <> 'deleted';
CREATE INDEX IF NOT EXISTS managed_postgres_bindings_account_app_idx
    ON managed_postgres_bindings(account_id,app_id) WHERE state <> 'deleted';

CREATE OR REPLACE FUNCTION guard_managed_postgres_binding_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM managed_postgres_databases database
        JOIN apps app ON app.id = NEW.app_id
        WHERE database.id = NEW.database_id
          AND database.account_id = NEW.account_id
          AND app.account_id = NEW.account_id
          AND database.state <> 'deleted'
          AND app.status <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'managed postgres binding owner mismatch or parent is deleted'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_binding_owner';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_binding_owner_guard ON managed_postgres_bindings;
CREATE TRIGGER managed_postgres_binding_owner_guard
    BEFORE INSERT OR UPDATE OF account_id,database_id,app_id ON managed_postgres_bindings
    FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_binding_owner();

CREATE OR REPLACE FUNCTION guard_managed_postgres_database_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.state = 'deleted' AND EXISTS (
        SELECT 1 FROM managed_postgres_bindings WHERE database_id = NEW.id AND state <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'managed postgres database has active bindings'
            USING ERRCODE = '23514', CONSTRAINT = 'managed_postgres_database_has_bindings';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_database_bindings_guard ON managed_postgres_databases;
CREATE TRIGGER managed_postgres_database_bindings_guard
    BEFORE UPDATE OF state ON managed_postgres_databases
    FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_database_bindings();

CREATE OR REPLACE FUNCTION guard_app_managed_postgres_bindings() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'deleted' AND EXISTS (
        SELECT 1 FROM managed_postgres_bindings WHERE app_id = NEW.id AND state <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'app has managed postgres bindings'
            USING ERRCODE = '23514', CONSTRAINT = 'app_has_managed_postgres_bindings';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS app_managed_postgres_bindings_guard ON apps;
CREATE TRIGGER app_managed_postgres_bindings_guard
    BEFORE UPDATE OF status ON apps FOR EACH ROW EXECUTE FUNCTION guard_app_managed_postgres_bindings();

CREATE OR REPLACE FUNCTION guard_account_managed_postgres_databases() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'deleted_pending' AND EXISTS (
        SELECT 1 FROM managed_postgres_databases WHERE account_id = NEW.id AND state <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'account has managed postgres databases'
            USING ERRCODE = '23514', CONSTRAINT = 'account_has_managed_postgres_databases';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS account_managed_postgres_databases_guard ON accounts;
CREATE TRIGGER account_managed_postgres_databases_guard
    BEFORE UPDATE OF status ON accounts FOR EACH ROW EXECUTE FUNCTION guard_account_managed_postgres_databases();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS account_managed_postgres_databases_guard ON accounts;
DROP FUNCTION IF EXISTS guard_account_managed_postgres_databases();
DROP TRIGGER IF EXISTS app_managed_postgres_bindings_guard ON apps;
DROP FUNCTION IF EXISTS guard_app_managed_postgres_bindings();
DROP TRIGGER IF EXISTS managed_postgres_database_bindings_guard ON managed_postgres_databases;
DROP FUNCTION IF EXISTS guard_managed_postgres_database_bindings();
DROP TRIGGER IF EXISTS managed_postgres_binding_owner_guard ON managed_postgres_bindings;
DROP FUNCTION IF EXISTS guard_managed_postgres_binding_owner();
DROP TABLE IF EXISTS managed_postgres_bindings;
DROP TABLE IF EXISTS managed_postgres_databases;
-- +goose StatementEnd
