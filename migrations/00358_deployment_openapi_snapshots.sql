-- filename: 00358_deployment_openapi_snapshots.sql
-- +goose Up
-- +goose StatementBegin

-- 00358_deployment_openapi_snapshots.sql — ADR-121 (issue: API
-- contract diff). Per-deployment snapshot of the projected customer
-- OpenAPI surface, captured atomically when a deployment transitions
-- to status='live'. The PR-C gate (PATCH /v1/deployments/{id} on
-- scope="prod") compares two snapshots and rejects the promotion
-- when the differ emits a SeverityError break.
--
-- Schema rationale (matches ADR §"Schema"):
--
--   * deployment_id is the PRIMARY KEY and a FK to deployments(id)
--     ON DELETE CASCADE. One snapshot row per deployment; UPSERT
--     semantics in the capture path (PR-B's markDeploymentLiveTx)
--     allow a re-capture on a status re-transition without a
--     truncated history. Same shape as the dns_poller's
--     domain_doctor_observations PK (migrations/00313).
--
--   * app_id is denormalised for the LatestOpenAPISnapshotForScope
--     index lookup; FK to apps(id) ON DELETE CASCADE so a hard
--     app purge sweeps its snapshot history. (Soft-delete via the
--     apps.status column is the common path; the cascade is the
--     defence-in-depth for a manual SQL cleanup.)
--
--   * snapshot is the canonical JSON form of the projected
--     pkg/openapidiff.Spec, produced by
--     pkg/openapidiff.MarshalSnapshot in PR-A. Stored as jsonb so
--     a future PR can render path-level previews without a
--     re-project from edge rules.
--
--   * sha256 is the hex-64 SHA-256 of the canonical JSON bytes.
--     Deterministic for replay / drift detection. The CHECK
--     constraint enforces the 64-hex-char shape so a corrupted
--     row fails the capture writer at insert time, not at query
--     time.
--
--   * schema_version starts at 1; bump on a breaking serializer
--     change. The loader (PR-C) reads schema_version and selects
--     the right deserializer. A bump is a separate migration.
--
--   * captured_at is the wall-clock timestamp the Status='live'
--     transition was written. ORDER BY ... DESC on the index
--     gives LatestOpenAPISnapshotForScope in O(1) per (app, scope).
--
--   * scope uses the same regex as deployments_scope_shape
--     (migrations/00213) so cross-table consistency is provable.
--     A scope value that passes the deploys check but fails this
--     CHECK is a bug; the migration test pins both regexes.
--
-- Replay-safety: CREATE TABLE IF NOT EXISTS / CREATE INDEX IF
-- NOT EXISTS (the canonical pattern from migrations/00053, 00287,
-- 00313). The replay_safety_test.go harness applies each
-- migration twice in a single tx and pins the second pass as a
-- no-op. The Down migration is forward-only (SELECT 1) because
-- the table is purely additive telemetry / audit — no data loss
-- on rollback.

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
-- Forward-only by design (mirrors 00313 + 00287 + 00229: reverting
-- would orphan any rows the PR-B capture writer wrote between this
-- migration's apply and the rollback. Drop the table
-- unconditionally on downgrade only if the operator explicitly
-- requests it; the default Down is a no-op sentinel so a replay
-- lands on the CREATE, not the drop.)
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
