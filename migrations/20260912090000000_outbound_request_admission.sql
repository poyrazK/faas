-- filename: 20260912090000000_outbound_request_admission.sql

-- +goose Up
-- +goose StatementBegin

-- An integration is an explicit, fixed-origin egress policy. Credentials are
-- represented by a digest; the raw token never enters this schema.
CREATE TABLE IF NOT EXISTS outbound_integrations (
    id                  uuid PRIMARY KEY,
    account_id          uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name                text NOT NULL CHECK (length(name) BETWEEN 1 AND 127),
    origin              text NOT NULL CHECK (origin ~ '^https://[^/?#]+(/[^?#]*)?$'),
    token_hash          bytea NOT NULL CHECK (octet_length(token_hash) = 32),
    rate_per_second     double precision NOT NULL CHECK (rate_per_second > 0),
    burst               integer NOT NULL CHECK (burst > 0),
    max_in_flight       integer NOT NULL CHECK (max_in_flight > 0),
    request_timeout_ms  integer NOT NULL CHECK (request_timeout_ms > 0),
    enabled             boolean NOT NULL DEFAULT true,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, name)
);

CREATE TABLE IF NOT EXISTS outbound_integration_apps (
    integration_id      uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    app_id              uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    created_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, app_id)
);
CREATE INDEX IF NOT EXISTS outbound_integration_apps_app_idx
    ON outbound_integration_apps (app_id, integration_id);

CREATE OR REPLACE FUNCTION guard_outbound_integration_app_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM outbound_integrations integration
        JOIN apps app ON app.id = NEW.app_id
        WHERE integration.id = NEW.integration_id
          AND integration.account_id = app.account_id
          AND app.status <> 'deleted'
    ) THEN
        RAISE EXCEPTION 'outbound integration app owner mismatch or app is deleted'
            USING ERRCODE = '23514', CONSTRAINT = 'outbound_integration_app_owner';
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS outbound_integration_app_owner_guard ON outbound_integration_apps;
CREATE TRIGGER outbound_integration_app_owner_guard
    BEFORE INSERT OR UPDATE OF integration_id,app_id ON outbound_integration_apps
    FOR EACH ROW EXECUTE FUNCTION guard_outbound_integration_app_owner();

-- One row per integration is the globally contended token bucket. Lease rows
-- make max_in_flight enforceable across gateway processes and expire if a
-- process disappears before it can release a completed request.
CREATE TABLE IF NOT EXISTS outbound_admission_state (
    integration_id  uuid PRIMARY KEY REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    tokens          double precision NOT NULL CHECK (tokens >= 0),
    last_refill     timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS outbound_admission_leases (
    lease_id        uuid PRIMARY KEY,
    integration_id  uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbound_admission_leases_expiry_idx
    ON outbound_admission_leases (integration_id, expires_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS outbound_admission_leases_expiry_idx;
DROP TABLE IF EXISTS outbound_admission_leases;
DROP TABLE IF EXISTS outbound_admission_state;
DROP TRIGGER IF EXISTS outbound_integration_app_owner_guard ON outbound_integration_apps;
DROP FUNCTION IF EXISTS guard_outbound_integration_app_owner();
DROP INDEX IF EXISTS outbound_integration_apps_app_idx;
DROP TABLE IF EXISTS outbound_integration_apps;
DROP TABLE IF EXISTS outbound_integrations;
-- +goose StatementEnd
