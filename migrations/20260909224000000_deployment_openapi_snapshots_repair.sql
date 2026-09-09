-- filename: 20260909224000000_deployment_openapi_snapshots_repair.sql
-- +goose Up
-- +goose StatementBegin

-- Forward repair for production databases that recorded legacy migration 358
-- before that slot was reassigned to deployment_openapi_snapshots. Goose
-- correctly refuses to replay an applied version even when its source file
-- later changes, so only a new append-only version can restore the schema.
CREATE TABLE IF NOT EXISTS deployment_openapi_snapshots (
    deployment_id  uuid        PRIMARY KEY
                    REFERENCES deployments(id) ON DELETE CASCADE,
    app_id         uuid        NOT NULL
                    REFERENCES apps(id) ON DELETE CASCADE,
    scope          text        NOT NULL,
    snapshot       jsonb       NOT NULL,
    sha256         text        NOT NULL,
    schema_version int         NOT NULL DEFAULT 1,
    captured_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deployment_openapi_snapshots_scope_shape CHECK (
        scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
    ),
    CONSTRAINT deployment_openapi_snapshots_sha256_shape CHECK (
        sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT deployment_openapi_snapshots_schema_version_positive CHECK (
        schema_version >= 1
    )
);

CREATE INDEX IF NOT EXISTS deployment_openapi_snapshots_app_scope_idx
    ON deployment_openapi_snapshots (app_id, scope, captured_at DESC);

-- +goose StatementEnd

-- +goose Down
-- This repair is forward-only. Removing the table would destroy snapshots
-- captured after the repair and reintroduce the production failure.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
