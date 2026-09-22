-- filename: 20260922175020068_deployment_provenance.sql

-- +goose Up
-- +goose StatementBegin

-- Preserve the platform-authored deployment identity at the durable
-- observability boundary. Empty strings are intentional: older gateways and
-- requests that fail before target resolution remain valid during rollout.

ALTER TABLE app_errors
    ADD COLUMN IF NOT EXISTS last_instance_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_node_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_region text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_commit_sha text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_deployment_tag text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_deployment_created_at text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_image_digest text NOT NULL DEFAULT '';

ALTER TABLE app_error_requests
    ADD COLUMN IF NOT EXISTS instance_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS node_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commit_sha text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_tag text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_created_at text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS image_digest text NOT NULL DEFAULT '';

ALTER TABLE request_telemetry
    ADD COLUMN IF NOT EXISTS node_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS commit_sha text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_tag text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS deployment_created_at text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS image_digest text NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE request_telemetry
    DROP COLUMN IF EXISTS image_digest,
    DROP COLUMN IF EXISTS deployment_created_at,
    DROP COLUMN IF EXISTS deployment_tag,
    DROP COLUMN IF EXISTS commit_sha,
    DROP COLUMN IF EXISTS region,
    DROP COLUMN IF EXISTS node_id;

ALTER TABLE app_error_requests
    DROP COLUMN IF EXISTS image_digest,
    DROP COLUMN IF EXISTS deployment_created_at,
    DROP COLUMN IF EXISTS deployment_tag,
    DROP COLUMN IF EXISTS commit_sha,
    DROP COLUMN IF EXISTS region,
    DROP COLUMN IF EXISTS node_id,
    DROP COLUMN IF EXISTS instance_id;

ALTER TABLE app_errors
    DROP COLUMN IF EXISTS last_image_digest,
    DROP COLUMN IF EXISTS last_deployment_created_at,
    DROP COLUMN IF EXISTS last_deployment_tag,
    DROP COLUMN IF EXISTS last_commit_sha,
    DROP COLUMN IF EXISTS last_region,
    DROP COLUMN IF EXISTS last_node_id,
    DROP COLUMN IF EXISTS last_instance_id;

-- +goose StatementEnd
