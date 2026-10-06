-- +goose Up
-- ADR-590: retain the original tracked capability roster before private
-- provider IO. Nothing here infers native drainage from time or request counts.
CREATE TABLE project_environment_clone_object_grant_revocations (
    operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
    source_bucket_id uuid NOT NULL CHECK (source_bucket_id <> '00000000-0000-0000-0000-000000000000'),
    request_id uuid NOT NULL UNIQUE CHECK (request_id <> '00000000-0000-0000-0000-000000000000'),
    plan jsonb NOT NULL CHECK (jsonb_typeof(plan) = 'object'),
    plan_sha256 text NOT NULL CHECK (plan_sha256 ~ '^[a-f0-9]{64}$'),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved', 'dispatched', 'drained')),
    revocation_id text NOT NULL DEFAULT '' CHECK (length(revocation_id) <= 512),
    retained_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    request_started_at timestamptz,
    observed_at timestamptz,
    drained_at timestamptz,
    PRIMARY KEY (operation_id, source_bucket_id),
    CHECK ((state = 'reserved') = (request_started_at IS NULL)),
    CHECK ((state = 'drained') = (drained_at IS NOT NULL)),
    CHECK ((observed_at IS NULL) = (revocation_id = '')),
    CHECK (state <> 'drained' OR observed_at IS NOT NULL),
    CHECK (observed_at IS NULL OR request_started_at IS NOT NULL),
    CHECK (request_started_at IS NULL OR request_started_at >= retained_at),
    CHECK (observed_at IS NULL OR observed_at >= request_started_at),
    CHECK (drained_at IS NULL OR drained_at >= request_started_at)
);

-- +goose Down
DROP TABLE project_environment_clone_object_grant_revocations;
