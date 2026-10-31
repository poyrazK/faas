-- filename: 20261031143926121_managed_postgres_restore_slot.sql
-- migration-compat: expand

-- ADR-20261031123259: a point-in-time restore may reserve one database past
-- the plan's limit. The database that takes that slot records when it ends;
-- it becomes an ordinary database when the account has room again, and its
-- delete is scheduled when the slot ends without room. An older apid ignores
-- the column: it neither grants nor ends a slot.
--
-- The column is nullable with no default, so adding it writes no rows.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

ALTER TABLE managed_postgres_databases ADD COLUMN IF NOT EXISTS restore_slot_until timestamptz;

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

ALTER TABLE managed_postgres_databases DROP COLUMN IF EXISTS restore_slot_until;

RESET lock_timeout;
