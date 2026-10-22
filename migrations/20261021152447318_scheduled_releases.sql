-- migration-compat: expand
-- filename: 20261021152447318_scheduled_releases.sql
--
-- ADR-20261021152201: scheduled releases. A production release built ahead
-- of time waits live at 0% traffic until release_at, when apid starts the
-- rollout recorded here. An app has at most one pending schedule.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS scheduled_releases (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 release_at timestamptz NOT NULL,
 canary jsonb CHECK (canary IS NULL OR (jsonb_typeof(canary) = 'object' AND octet_length(canary::text) <= 4096)),
 notify_channel_ids uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(notify_channel_ids) <= 5),
 override_freeze text NOT NULL DEFAULT '' CHECK (length(override_freeze) <= 200),
 override_freeze_ids uuid[] NOT NULL DEFAULT '{}' CHECK (cardinality(override_freeze_ids) <= 50),
 state text NOT NULL DEFAULT 'scheduled' CHECK (state IN ('scheduled','started','blocked','expired','cancelled','superseded')),
 outcome_code text NOT NULL DEFAULT '' CHECK (length(outcome_code) <= 64),
 outcome_detail text NOT NULL DEFAULT '' CHECK (length(outcome_detail) <= 500),
 scheduled_by text NOT NULL CHECK (length(scheduled_by) BETWEEN 1 AND 320),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 started_at timestamptz,
 CHECK ((state = 'started') = (started_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS scheduled_releases_one_pending ON scheduled_releases(app_id) WHERE state = 'scheduled';
CREATE INDEX IF NOT EXISTS scheduled_releases_due ON scheduled_releases(release_at) WHERE state = 'scheduled';
CREATE INDEX IF NOT EXISTS scheduled_releases_deployment ON scheduled_releases(deployment_id, created_at);
CREATE INDEX IF NOT EXISTS scheduled_releases_account ON scheduled_releases(account_id, created_at);
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TABLE IF EXISTS scheduled_releases;
-- +goose StatementEnd
