-- migration-compat: expand
-- filename: 20261015162437648_operator_alert_triage.sql

-- +goose Up
ALTER TABLE operator_alert_episodes
    ADD COLUMN IF NOT EXISTS triage_revision bigint NOT NULL DEFAULT 0 CHECK (triage_revision >= 0),
    ADD COLUMN IF NOT EXISTS triage_acknowledged boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS triage_owner_id uuid,
    ADD COLUMN IF NOT EXISTS triage_snoozed_until timestamptz,
    ADD COLUMN IF NOT EXISTS triage_updated_at timestamptz;

-- +goose Down
ALTER TABLE operator_alert_episodes
    DROP COLUMN IF EXISTS triage_updated_at,
    DROP COLUMN IF EXISTS triage_snoozed_until,
    DROP COLUMN IF EXISTS triage_owner_id,
    DROP COLUMN IF EXISTS triage_acknowledged,
    DROP COLUMN IF EXISTS triage_revision;
