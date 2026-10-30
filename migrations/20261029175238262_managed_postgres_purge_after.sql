-- filename: 20261029175238262_managed_postgres_purge_after.sql
-- migration-compat: expand

-- ADR-20261029174950: a customer delete is scheduled for a 7-day window. The
-- database stays 'deleting' and records when the window ends; the lifecycle
-- reconciler deletes the provider resource once retry_at reaches it, and an
-- undo is allowed only while it is in the future. An immediate delete clears
-- it, so undo can never race a provider delete. An older apid ignores the
-- column and sees an ordinary deleting row whose retry time is a week away.
--
-- The column is nullable with no default, so adding it writes no rows.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS purge_after timestamptz;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE managed_postgres_databases DROP COLUMN IF EXISTS purge_after;

RESET lock_timeout;
