-- filename: 20261111124927540_secret_grants_and_open_audit.sql
-- migration-compat: expand
-- ADR-20261110210000 §3–4, slice 3a: secretd's records.
--
-- secret_grants: an app's owner node lets a peer node open the app's
-- secrets for a while (the owner places instances on peers for snapshot
-- locality and replica spread). Only secretd writes it, after checking over
-- mutual TLS that the caller is the app's owner; compute cannot.
--
-- secret_open_audit: every open secretd performed, so a node compromise
-- becomes "rotate the secrets of exactly these apps". Append-only for every
-- runtime login.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS secret_grants (
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
    granted_by_node_id uuid NOT NULL,
    granted_at timestamp with time zone NOT NULL DEFAULT now(),
    expires_at timestamp with time zone NOT NULL,
    PRIMARY KEY (app_id, node_id),
    CONSTRAINT secret_grants_expiry_check CHECK (expires_at > granted_at)
);
CREATE INDEX IF NOT EXISTS secret_grants_expires_idx ON secret_grants (expires_at);

-- No foreign keys: the record outlives the app and the node.
CREATE TABLE IF NOT EXISTS secret_open_audit (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id uuid NOT NULL,
    app_id uuid NOT NULL,
    via text NOT NULL CHECK (via IN ('owner', 'grant')),
    entries integer NOT NULL CHECK (entries >= 0),
    opened_at timestamp with time zone NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS secret_open_audit_node_idx ON secret_open_audit (node_id, opened_at);
CREATE INDEX IF NOT EXISTS secret_open_audit_app_idx ON secret_open_audit (app_id, opened_at);

-- Default privileges give faas_runtime DML on new tables. Compute must not
-- grant itself access or erase the record; runtime-roles.sql repeats this
-- and grants secretd's own login what it needs.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON secret_grants FROM faas_runtime;
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON secret_open_audit FROM faas_runtime;
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS secret_open_audit;
DROP TABLE IF EXISTS secret_grants;
