-- migration-compat: expand
-- +goose Up

-- Keep the cleanup record after the source database is purged. API access
-- still requires authorization against the live source ownership record.
CREATE TABLE native_postgres_exports (
    id uuid PRIMARY KEY,
    database_id uuid NOT NULL,
    owner_id text NOT NULL,
    org_id text NOT NULL,
    generation bigint NOT NULL CHECK (generation > 0),
    state text NOT NULL CHECK (state IN ('queued', 'running', 'ready', 'failed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    lease_until timestamptz,
    lease_token uuid,
    manifest jsonb,
    artifact_token uuid,
    CHECK ((state = 'ready') = (manifest IS NOT NULL AND artifact_token IS NOT NULL)),
    CHECK ((state = 'running') = (lease_until IS NOT NULL AND lease_token IS NOT NULL))
);

CREATE INDEX native_postgres_exports_scope_idx ON native_postgres_exports (owner_id, org_id, expires_at);
CREATE INDEX native_postgres_exports_queued_idx ON native_postgres_exports (created_at) WHERE state IN ('queued', 'running');
CREATE INDEX native_postgres_exports_expires_idx ON native_postgres_exports (expires_at);
CREATE UNIQUE INDEX native_postgres_exports_active_idx ON native_postgres_exports (database_id) WHERE state IN ('queued', 'running');

-- Reserve an immutable object for each lease before starting the upload.
-- An interrupted or superseded attempt can be collected independently.
CREATE TABLE native_postgres_export_objects (
    export_id uuid NOT NULL,
    token uuid PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX native_postgres_export_objects_expiry_idx ON native_postgres_export_objects (expires_at);

-- +goose Down
DROP TABLE native_postgres_export_objects;
DROP TABLE native_postgres_exports;
