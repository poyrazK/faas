-- filename: 20261021090000001_rehearsal_captured_queue_messages.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Queue sends and delayed tasks an enrolled app requests through the run-local
-- platform API are captured here instead of being enqueued
-- (ADR-20261021090000). Rows are run evidence: they are written before the
-- run seals, never updated, and leave with the policy.
CREATE TABLE IF NOT EXISTS scenario_captured_queue_messages (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 run_id text NOT NULL CHECK (run_id ~ '^[0-9a-f]{32}$'),
 app_id uuid NOT NULL REFERENCES scenario_test_action_policies(app_id) ON DELETE CASCADE,
 kind text NOT NULL CHECK (kind IN ('queue', 'delayed_task')),
 -- workload:<name> for a member of the same run, otherwise app:<slug>.
 target text NOT NULL CHECK (target ~ '^(workload|app):[a-z0-9][a-z0-9-]{0,62}$'),
 queue_name text NOT NULL DEFAULT '' CHECK (char_length(queue_name) <= 256),
 -- delay:<seconds> or absolute; wall-clock instants never compare.
 schedule text NOT NULL DEFAULT '' CHECK (schedule = '' OR schedule = 'absolute' OR schedule ~ '^delay:[0-9]{1,8}$'),
 -- The 64 KiB bound applies to the request bytes; jsonb's text form adds
 -- separator whitespace, so the stored bound leaves room for it.
 message jsonb CHECK (message IS NULL OR octet_length(message::text) <= 262144),
 oversized boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (oversized = (message IS NULL)),
 CHECK ((kind = 'delayed_task') = (schedule <> '') OR oversized)
);
CREATE INDEX IF NOT EXISTS scenario_captured_queue_messages_run_idx ON scenario_captured_queue_messages(account_id, run_id, created_at, id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS(SELECT 1 FROM scenario_captured_queue_messages)
 THEN RAISE EXCEPTION 'captured rehearsal queue messages are retained run evidence'; END IF; END $$;
-- +goose StatementEnd
DROP TABLE scenario_captured_queue_messages;
