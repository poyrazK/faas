-- migration-compat: expand
-- filename: 20261124213833100_mcp_oauth_consumers.sql

-- ADR-20261124094745: self-serve consumers for MCP servers. Sign-up can
-- offer a free plan that needs no card, and can enroll callers signed in
-- through an MCP server's oauth-proxy issuer as consumers on it (their
-- identities are in 20261124213833200). Older apid builds ignore the columns.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_consumer_signup_settings
  ADD COLUMN IF NOT EXISTS oauth_consumers boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS free_plan_id uuid;

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_signup_settings_oauth_free_chk') THEN
    -- Every existing row holds the false/NULL defaults; new writes are checked at once.
    ALTER TABLE api_consumer_signup_settings
      ADD CONSTRAINT api_consumer_signup_settings_oauth_free_chk
        CHECK (NOT oauth_consumers OR free_plan_id IS NOT NULL) NOT VALID;
  END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN api_consumer_signup_settings.oauth_consumers IS
  'Callers signed in through the app''s MCP oauth-proxy issuer become consumers on free_plan_id (ADR-20261124094745).';
COMMENT ON COLUMN api_consumer_signup_settings.free_plan_id IS
  'Offered plan that charges nothing: joining it skips Checkout (ADR-20261124094745).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE api_consumer_signup_settings
  DROP CONSTRAINT IF EXISTS api_consumer_signup_settings_oauth_free_chk;
ALTER TABLE api_consumer_signup_settings
  DROP COLUMN IF EXISTS free_plan_id,
  DROP COLUMN IF EXISTS oauth_consumers;
