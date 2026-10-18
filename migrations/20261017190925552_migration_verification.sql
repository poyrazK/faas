-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS migration_verifications (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL,
 version bigint NOT NULL CHECK (version > 0),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
 CONSTRAINT migration_verification_report_check CHECK (COALESCE(
  jsonb_typeof(snapshot->'Report') = 'object'
  AND snapshot->'Report'->>'status' IN ('pending','passed','failed','blocked','expired')
  AND snapshot->'Report'->>'phase' IN ('dependencies','startup','park','drain','wake','waking','cold_smoke','cold_dependencies','complete')
  AND snapshot->'Report'->>'id' = id::text, false))
);
CREATE INDEX IF NOT EXISTS migration_verifications_app_idx ON migration_verifications(account_id,app_id,created_at DESC,id DESC);

-- +goose Down
DROP TABLE migration_verifications;
