-- migration-compat: expand
-- filename: 20261019071924383_release_freezes.sql
--
-- ADR-20261019071733: deploy freezes and release windows. A freeze belongs to
-- an account and covers one app or, with app_id null, all of the account's
-- apps. While one is active apid refuses new production deployments unless the
-- request carries an audited override. 'after_rollback' rows are policies:
-- apid turns an automatic rollback of a covered app into a 'window' freeze
-- with source 'automatic_rollback'. Lifted rows are kept for the audit trail.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS release_freezes (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid REFERENCES apps(id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('window','weekly','after_rollback')),
 starts_at timestamptz,
 ends_at timestamptz,
 weekly_days smallint CHECK (weekly_days IS NULL OR weekly_days BETWEEN 1 AND 127),
 weekly_start_minute smallint CHECK (weekly_start_minute IS NULL OR weekly_start_minute BETWEEN 0 AND 1439),
 weekly_duration_minutes integer CHECK (weekly_duration_minutes IS NULL OR weekly_duration_minutes BETWEEN 1 AND 10080),
 time_zone text CHECK (time_zone IS NULL OR (length(time_zone) BETWEEN 1 AND 64)),
 after_rollback_minutes integer CHECK (after_rollback_minutes IS NULL OR after_rollback_minutes BETWEEN 1 AND 1440),
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 200),
 source text NOT NULL DEFAULT 'manual' CHECK (source IN ('manual','automatic_rollback')),
 source_deployment_id uuid,
 created_by text NOT NULL CHECK (length(created_by) BETWEEN 1 AND 320),
 created_at timestamptz NOT NULL DEFAULT now(),
 lifted_at timestamptz,
 lifted_by text CHECK (lifted_by IS NULL OR length(lifted_by) BETWEEN 1 AND 320),
 CHECK (kind <> 'window' OR (starts_at IS NOT NULL AND (ends_at IS NULL OR ends_at > starts_at)
   AND weekly_days IS NULL AND after_rollback_minutes IS NULL)),
 CHECK (kind <> 'weekly' OR (weekly_days IS NOT NULL AND weekly_start_minute IS NOT NULL
   AND weekly_duration_minutes IS NOT NULL AND time_zone IS NOT NULL
   AND starts_at IS NULL AND ends_at IS NULL AND after_rollback_minutes IS NULL)),
 CHECK (kind <> 'after_rollback' OR (after_rollback_minutes IS NOT NULL
   AND starts_at IS NULL AND ends_at IS NULL AND weekly_days IS NULL)),
 CHECK (source = 'manual' OR (kind = 'window' AND app_id IS NOT NULL AND source_deployment_id IS NOT NULL)),
 CHECK ((lifted_at IS NULL) = (lifted_by IS NULL))
);
CREATE INDEX IF NOT EXISTS release_freezes_unlifted
 ON release_freezes(account_id, created_at, id) WHERE lifted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS release_freezes_one_per_rollback
 ON release_freezes(source_deployment_id) WHERE source = 'automatic_rollback';
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TABLE IF EXISTS release_freezes;
-- +goose StatementEnd
