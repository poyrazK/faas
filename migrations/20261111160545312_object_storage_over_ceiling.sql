-- filename: 20261111160545312_object_storage_over_ceiling.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261111123000: one row per billing subject (an account, or an org that
-- owns its resources) that stores more object-storage bytes than its ceiling.
-- apid's sweep is the only writer: it inserts the row when it first sees the
-- subject over, refreshes the observation while it stays over, and deletes the
-- row once the subject fits again. deletion_scheduled_at is fixed at insert.
-- No foreign key: the subject is an account or an org, as in
-- object_storage_ceiling_grants. A new table, so older apids ignore it.
CREATE TABLE IF NOT EXISTS object_storage_over_ceiling (
    subject_id            uuid PRIMARY KEY,
    over_ceiling_since    timestamptz NOT NULL CHECK (isfinite(over_ceiling_since)),
    deletion_scheduled_at timestamptz NOT NULL CHECK (isfinite(deletion_scheduled_at) AND deletion_scheduled_at > over_ceiling_since),
    stored_bytes          bigint NOT NULL CHECK (stored_bytes > 0),
    ceiling_bytes         bigint NOT NULL CHECK (ceiling_bytes >= 0 AND ceiling_bytes < stored_bytes),
    observed_at           timestamptz NOT NULL CHECK (isfinite(observed_at) AND observed_at >= over_ceiling_since),
    -- Notices sent so far: the start notice and the reminders
    -- (api.ObjectStorageOverCeilingNoticeDays), in order.
    notices_sent          smallint NOT NULL DEFAULT 0 CHECK (notices_sent BETWEEN 0 AND 3)
);
CREATE INDEX IF NOT EXISTS object_storage_over_ceiling_due_idx ON object_storage_over_ceiling (deletion_scheduled_at);
COMMENT ON TABLE object_storage_over_ceiling IS
    'ADR-20261111123000: billing subjects storing more object-storage bytes than their ceiling, with the date their excess is deleted; written only by apid.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS object_storage_over_ceiling;
-- +goose StatementEnd
