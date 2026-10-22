-- filename: 20261021225217539_operator_database_move_intent.sql
-- migration-compat: expand

-- ADR-1027: operators move, restart and switch over native databases
-- through the durable operator-intent queue. apid records the request;
-- schedd's database scheduler runs it. target_id is the database id; a
-- move's metadata names the target node.
--
-- The kinds widen operator_intents_kind_check without blocking writes: this
-- migration adds the wider check NOT VALID under a new name, and the next
-- one (20261021225218164) validates it, drops the narrow one and gives the
-- wider one the old name.
-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'operator_intents'::regclass AND conname = 'operator_intents_kind_check_next') THEN
  ALTER TABLE operator_intents ADD CONSTRAINT operator_intents_kind_check_next
   CHECK (kind IN ('force_park', 'force_cold_boot', 'force_restart', 'node_drain', 'node_force_drain', 'node_activate', 'node_retire',
    'database_move', 'database_restart', 'database_switchover')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE operator_intents DROP CONSTRAINT IF EXISTS operator_intents_kind_check_next;
-- +goose StatementEnd
