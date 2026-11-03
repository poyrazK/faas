-- filename: 20261103090000000_environment_branch_captures.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks deployments briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261031193635 slice 5a: the credential-free capture of a parent
-- deployment, the only memory an environment branch may restore. Its boot
-- received placeholder values for every managed binding secret and ran under a
-- non-production workload environment, so the capture holds no parent
-- credential. It lives in its own table, not in snapshots, so that no
-- production snapshot lookup, restore check or replica path can ever select
-- it. A new table, so older daemons keep working.
CREATE TABLE IF NOT EXISTS environment_branch_captures (
    id                 uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id      uuid        NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    state              text        NOT NULL DEFAULT 'requested',
    attempts           int         NOT NULL DEFAULT 0,
    lease_token        uuid,
    lease_until        timestamptz,
    failure            text        NOT NULL DEFAULT '',
    -- The published artifact; set only in state ready.
    fc_version         text        NOT NULL DEFAULT '',
    storage_key        text        NOT NULL DEFAULT '',
    base_image_version text        NOT NULL DEFAULT '',
    cpu_template       text        NOT NULL DEFAULT '',
    cpu_model          text        NOT NULL DEFAULT '',
    mem_bytes          bigint      NOT NULL DEFAULT 0,
    disk_bytes         bigint      NOT NULL DEFAULT 0,
    stored_bytes       bigint      NOT NULL DEFAULT 0,
    requested_at       timestamptz NOT NULL DEFAULT now(),
    ready_at           timestamptz,
    CONSTRAINT environment_branch_captures_one_per_deployment UNIQUE (deployment_id),
    CONSTRAINT environment_branch_captures_state CHECK (state IN ('requested', 'ready', 'failed')),
    CONSTRAINT environment_branch_captures_lease CHECK ((lease_token IS NULL) = (lease_until IS NULL)),
    CONSTRAINT environment_branch_captures_attempts CHECK (attempts >= 0),
    CONSTRAINT environment_branch_captures_bytes CHECK (mem_bytes >= 0 AND disk_bytes >= 0 AND stored_bytes >= 0),
    CONSTRAINT environment_branch_captures_failure CHECK (octet_length(failure) <= 512),
    CONSTRAINT environment_branch_captures_ready CHECK (
        (state = 'ready') = (storage_key <> '' AND fc_version <> '' AND ready_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS environment_branch_captures_requested_idx
    ON environment_branch_captures (requested_at) WHERE state = 'requested';
COMMENT ON TABLE environment_branch_captures IS
    'ADR-20261031193635 credential-free captures of parent deployments; only environment branches restore them, production never does.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS environment_branch_captures;
-- +goose StatementEnd
