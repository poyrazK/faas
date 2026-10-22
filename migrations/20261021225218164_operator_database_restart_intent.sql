-- filename: 20261021225218164_operator_database_restart_intent.sql
-- migration-compat: contract

-- ADR-1027: the second step of widening operator_intents_kind_check for the
-- native database intents (database_move, database_restart,
-- database_switchover; see 20261021225217539). It validates the wider check
-- without blocking writes, then drops the narrow one and gives the wider
-- one its name. Every step is skipped once done, so a replay keeps the
-- check. It is a contract migration: dropping and renaming the check is
-- a full release's to apply.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'operator_intents'::regclass AND conname = 'operator_intents_kind_check_next') THEN
  ALTER TABLE operator_intents VALIDATE CONSTRAINT operator_intents_kind_check_next;
  ALTER TABLE operator_intents DROP CONSTRAINT IF EXISTS operator_intents_kind_check;
  ALTER TABLE operator_intents RENAME CONSTRAINT operator_intents_kind_check_next TO operator_intents_kind_check;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DELETE FROM operator_intents WHERE kind IN ('database_move', 'database_restart', 'database_switchover');
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'operator_intents'::regclass AND conname = 'operator_intents_kind_check_next') THEN
  ALTER TABLE operator_intents RENAME CONSTRAINT operator_intents_kind_check TO operator_intents_kind_check_next;
  ALTER TABLE operator_intents ADD CONSTRAINT operator_intents_kind_check
   CHECK (kind IN ('force_park', 'force_cold_boot', 'force_restart', 'node_drain', 'node_force_drain', 'node_activate', 'node_retire'));
 END IF;
END $$;
-- +goose StatementEnd
