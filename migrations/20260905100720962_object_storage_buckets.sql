-- filename: 20260905100720962_object_storage_buckets.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS object_buckets (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id),
    app_id uuid NOT NULL REFERENCES apps(id),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 63),
    region text NOT NULL CHECK (length(region) BETWEEN 1 AND 63),
    backend_id text NOT NULL CHECK (length(backend_id) BETWEEN 1 AND 63),
    backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
    physical_name text NOT NULL UNIQUE,
    state text NOT NULL DEFAULT 'provisioning' CHECK (state IN ('provisioning','ready','deleting','deleted')),
    lease_token text,
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS object_buckets_name_idx ON object_buckets(app_id,scope,name) WHERE state <> 'deleted';
CREATE INDEX IF NOT EXISTS object_buckets_account_app_idx ON object_buckets(account_id,app_id);

-- Parent-row locking in reservation serializes creates with app deletion.
-- Protect all deletion paths, including reconciliation outside the HTTP API.
CREATE OR REPLACE FUNCTION guard_app_object_buckets() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status = 'deleted' AND EXISTS (SELECT 1 FROM object_buckets WHERE app_id = NEW.id AND state <> 'deleted') THEN
        RAISE EXCEPTION 'app has object buckets' USING ERRCODE = '23514', CONSTRAINT = 'app_has_object_buckets';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS app_object_buckets_guard ON apps;
CREATE TRIGGER app_object_buckets_guard BEFORE UPDATE OF status ON apps FOR EACH ROW EXECUTE FUNCTION guard_app_object_buckets();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_object_buckets_guard ON apps;
DROP FUNCTION IF EXISTS guard_app_object_buckets();
DROP TABLE IF EXISTS object_buckets;
-- +goose StatementEnd
