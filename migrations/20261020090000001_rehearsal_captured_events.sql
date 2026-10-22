-- filename: 20261020090000001_rehearsal_captured_events.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Events an enrolled app publishes are captured here instead of entering the
-- account's event bus (ADR-20261020090000). Rows are run evidence: they are
-- written before the run seals, never updated, and leave with the policy.
CREATE TABLE IF NOT EXISTS scenario_captured_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
 app_id uuid NOT NULL REFERENCES scenario_test_action_policies(app_id) ON DELETE CASCADE,
 source text NOT NULL CHECK (char_length(source) BETWEEN 1 AND 256),
 type text NOT NULL CHECK (char_length(type) BETWEEN 1 AND 256),
 schema_version text NOT NULL DEFAULT '' CHECK (char_length(schema_version) <= 256),
 -- The 64 KiB bound applies to the published bytes; jsonb's text form adds
 -- separator whitespace, so the stored bound leaves room for it.
 data jsonb CHECK (data IS NULL OR octet_length(data::text) <= 262144),
 oversized boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (oversized = (data IS NULL))
);
CREATE INDEX IF NOT EXISTS scenario_captured_events_run_idx ON scenario_captured_events(account_id, run_id, created_at, id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_captured_events)
 THEN RAISE EXCEPTION 'captured rehearsal events are retained run evidence'; END IF; END $$;
-- +goose StatementEnd
DROP TABLE scenario_captured_events;
