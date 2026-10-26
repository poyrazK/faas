-- filename: 20261026151103427_integration_authors.sql
-- migration-compat: expand
-- ADR-20261026151103: a company app's alert rules, log drains and webhooks
-- carry its creator's account_id, as every company app row does, so the row
-- cannot say which member chose its destination. created_by_account_id
-- records the member who created it, so the organization can review and
-- pause what a departing member left behind. NULL is a row created before
-- this column, when only the app's creator could create one.
--
-- The previous release neither reads nor writes the column; its inserts
-- leave it NULL. Each table commits on its own, so no transaction holds
-- more than one table's lock (and the accounts row lock of its foreign key)
-- while it waits for the next.

-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS created_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE app_log_drains ADD COLUMN IF NOT EXISTS created_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE app_webhooks ADD COLUMN IF NOT EXISTS created_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE app_webhooks DROP COLUMN IF EXISTS created_by_account_id;

ALTER TABLE app_log_drains DROP COLUMN IF EXISTS created_by_account_id;

ALTER TABLE alert_rules DROP COLUMN IF EXISTS created_by_account_id;

RESET lock_timeout;
