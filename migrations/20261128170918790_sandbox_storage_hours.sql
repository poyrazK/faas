-- filename: 20261128170918790_sandbox_storage_hours.sql
-- migration-compat: expand
-- ADR-20261122132055 §8: the stored bytes of each account's paused sandbox
-- sessions, one row per account and hour, written by agentd's sweep (the
-- first sweep of an hour wins). Metered dark: nothing prices or bills it
-- until the GB-month price is set. The previous release never reads it.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS sandbox_storage_hours (
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 -- hour is the UTC hour the row covers, truncated.
 hour timestamptz NOT NULL,
 paused_sessions integer NOT NULL CHECK (paused_sessions >= 0),
 -- capture_bytes: the paused sessions' pause captures (memory and vmstate);
 -- volume_bytes: the data their newest volume backups hold.
 capture_bytes bigint NOT NULL CHECK (capture_bytes >= 0),
 volume_bytes bigint NOT NULL CHECK (volume_bytes >= 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (account_id, hour)
);

-- +goose Down
DROP TABLE IF EXISTS sandbox_storage_hours;
