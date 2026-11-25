-- migration-compat: expand
-- filename: 20261125194510100_mcp_account_tools.sql

-- ADR-20261125194510: with account_tools on, the gateway adds the
-- account_usage and account_plans tools to the app's MCP server for callers
-- it enrolled through OAuth. Older apid builds ignore the column.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_consumer_signup_settings
  ADD COLUMN IF NOT EXISTS account_tools boolean NOT NULL DEFAULT false;

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_signup_settings_account_tools_chk') THEN
    -- Every existing row holds the false default; new writes are checked at once.
    ALTER TABLE api_consumer_signup_settings
      ADD CONSTRAINT api_consumer_signup_settings_account_tools_chk
        CHECK (NOT account_tools OR oauth_consumers) NOT VALID;
  END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN api_consumer_signup_settings.account_tools IS
  'The gateway adds account_usage and account_plans tools to the app''s MCP server for OAuth consumers (ADR-20261125194510).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_consumer_signup_settings
  DROP CONSTRAINT IF EXISTS api_consumer_signup_settings_account_tools_chk;
ALTER TABLE api_consumer_signup_settings
  DROP COLUMN IF EXISTS account_tools;
