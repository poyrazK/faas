-- filename: 20261004153454875_deployment_route_policy_snapshots.sql

-- +goose Up
-- +goose StatementBegin

-- Immutable gateway edge-rule policy captured for each deployment when it
-- first transitions to live. This is separate from the deployment's OpenAPI
-- contract snapshot because gateway actions and selectors describe the
-- enforced edge policy.
CREATE TABLE IF NOT EXISTS deployment_route_policy_snapshots (
    deployment_id  uuid        PRIMARY KEY
                    REFERENCES deployments(id) ON DELETE CASCADE,
    app_id         uuid        NOT NULL
                    REFERENCES apps(id) ON DELETE CASCADE,
    scope          text        NOT NULL,
    snapshot       jsonb       NOT NULL,
    sha256         text        NOT NULL,
    schema_version int         NOT NULL DEFAULT 1,
    captured_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT deployment_route_policy_snapshots_scope_shape CHECK (
        scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$'
    ),
    CONSTRAINT deployment_route_policy_snapshots_sha256_shape CHECK (
        sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT deployment_route_policy_snapshots_schema_version_positive CHECK (
        schema_version >= 1
    )
);

CREATE INDEX IF NOT EXISTS deployment_route_policy_snapshots_app_scope_idx
    ON deployment_route_policy_snapshots (app_id, scope, captured_at DESC);
-- +goose StatementEnd

-- +goose Down
-- Forward-only: route policy is audit evidence and must not be silently lost.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
