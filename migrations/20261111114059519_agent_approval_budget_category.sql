-- migration-compat: contract
-- ADR-20261111114059: agent approval requests gain the category 'budget'.
-- The wider CHECK is added NOT VALID under a new name, validated, and only
-- then is the narrower one dropped, so no write is ever unchecked. The
-- previous release writes only the four older categories and never relies on
-- the narrower CHECK. Each step is safe to replay.
-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'agent_approval_requests'::regclass
                    AND conname = 'agent_approval_requests_category_budget_check') THEN
    ALTER TABLE agent_approval_requests
      ADD CONSTRAINT agent_approval_requests_category_budget_check
      CHECK (category IN ('delete', 'production', 'credentials', 'billing', 'budget')) NOT VALID;
  END IF;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE agent_approval_requests VALIDATE CONSTRAINT agent_approval_requests_category_budget_check;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE agent_approval_requests DROP CONSTRAINT IF EXISTS agent_approval_requests_category_check;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET lock_timeout = '5s';
DELETE FROM agent_approval_requests WHERE category = 'budget';
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint
                  WHERE conrelid = 'agent_approval_requests'::regclass
                    AND conname = 'agent_approval_requests_category_check') THEN
    ALTER TABLE agent_approval_requests
      ADD CONSTRAINT agent_approval_requests_category_check
      CHECK (category IN ('delete', 'production', 'credentials', 'billing')) NOT VALID;
  END IF;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
SET lock_timeout = '5s';
ALTER TABLE agent_approval_requests DROP CONSTRAINT IF EXISTS agent_approval_requests_category_budget_check;
-- +goose StatementEnd
