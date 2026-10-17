-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE migration_cutovers (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 version bigint NOT NULL CHECK (version > 0),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
 next_attempt_at timestamptz,
 CONSTRAINT migration_cutover_report_check CHECK (COALESCE(
  jsonb_typeof(snapshot->'Report') = 'object'
  AND snapshot->'Report'->>'status' IN ('prepared','checking','ready','attention','completed','cancelled','restored','blocked')
  AND snapshot->'Report'->>'id' = id::text, false))
);
CREATE INDEX migration_cutovers_app_idx ON migration_cutovers(account_id,app_id,created_at DESC,id DESC);
CREATE INDEX migration_cutovers_due_idx ON migration_cutovers(next_attempt_at,id) WHERE next_attempt_at IS NOT NULL;

-- +goose Down
DROP TABLE migration_cutovers;
