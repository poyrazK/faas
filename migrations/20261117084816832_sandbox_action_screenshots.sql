-- filename: 20261117084816832_sandbox_action_screenshots.sql
-- migration-compat: expand

-- +goose Up
-- The columns lock sandbox_actions briefly and rewrite no rows; their
-- CHECKs are NOT VALID, so the lock never scans the table.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261117065818 §5: a gregale_browser action's screenshot, kept in the
-- artifact store at screenshot_key until agentd's sweep deletes it a day
-- after the session ends and clears the key. Nullable, so older binaries
-- keep working. Its index is built concurrently by the next migration.
ALTER TABLE sandbox_actions
    ADD COLUMN IF NOT EXISTS screenshot_key text,
    ADD COLUMN IF NOT EXISTS screenshot_bytes bigint;
DO $shots$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'sandbox_actions'::regclass AND conname = 'sandbox_actions_screenshot_key_check') THEN
    ALTER TABLE sandbox_actions ADD CONSTRAINT sandbox_actions_screenshot_key_check
      CHECK (screenshot_key IS NULL OR screenshot_key ~ '^sandbox-screenshots/[0-9a-f-]{36}/[0-9a-f-]{36}/[0-9a-f-]{36}\.jpg$') NOT VALID;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'sandbox_actions'::regclass AND conname = 'sandbox_actions_screenshot_bytes_check') THEN
    ALTER TABLE sandbox_actions ADD CONSTRAINT sandbox_actions_screenshot_bytes_check
      CHECK (screenshot_bytes IS NULL OR screenshot_bytes BETWEEN 1 AND 1048576) NOT VALID;
  END IF;
END
$shots$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_actions
    DROP COLUMN IF EXISTS screenshot_bytes,
    DROP COLUMN IF EXISTS screenshot_key;
-- +goose StatementEnd
