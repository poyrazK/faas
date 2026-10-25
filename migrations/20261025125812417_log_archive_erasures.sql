-- migration-compat: expand
-- filename: 20261025125812417_log_archive_erasures.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- Archived runtime logs live in object storage keyed by instance only
-- (faas-logs/{instance}/...), so deleting an account's rows never reached
-- them. Before the grace sweep deletes an account, apid snapshots the
-- instances whose logs go with it, the same personal-org apps DeleteAccount
-- removes, and deletes each instance's objects. The snapshot belongs to one
-- deletion request: a later request for the same account takes a new one.
CREATE TABLE IF NOT EXISTS log_archive_erasures (
    account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    deletion_requested_at timestamptz NOT NULL CHECK (isfinite(deletion_requested_at)),
    snapshotted_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(snapshotted_at))
);
CREATE TABLE IF NOT EXISTS log_archive_erasure_instances (
    account_id uuid NOT NULL REFERENCES log_archive_erasures(account_id) ON DELETE CASCADE,
    instance_id uuid NOT NULL,
    erased_at timestamptz CHECK (erased_at IS NULL OR isfinite(erased_at)),
    PRIMARY KEY (account_id, instance_id)
);
CREATE INDEX IF NOT EXISTS log_archive_erasure_instances_pending_idx
    ON log_archive_erasure_instances (account_id, instance_id) WHERE erased_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS log_archive_erasure_instances;
DROP TABLE IF EXISTS log_archive_erasures;
-- +goose StatementEnd
