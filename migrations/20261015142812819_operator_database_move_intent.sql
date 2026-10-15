-- filename: 20261015142812819_operator_database_move_intent.sql

-- ADR-1027: operators move a native database between database nodes through
-- the durable operator-intent queue. apid records the request; schedd's
-- database scheduler parks the database, takes the move's lease and copies
-- its volume. target_id is the database id; metadata names the target node.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE operator_intents
    DROP CONSTRAINT IF EXISTS operator_intents_kind_check;

ALTER TABLE operator_intents
    ADD CONSTRAINT operator_intents_kind_check
        CHECK (kind IN (
            'force_park',
            'force_cold_boot',
            'force_restart',
            'node_drain',
            'node_force_drain',
            'node_activate',
            'node_retire',
            'database_move'
        ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM operator_intents WHERE kind = 'database_move';

ALTER TABLE operator_intents
    DROP CONSTRAINT IF EXISTS operator_intents_kind_check;

ALTER TABLE operator_intents
    ADD CONSTRAINT operator_intents_kind_check
        CHECK (kind IN (
            'force_park',
            'force_cold_boot',
            'force_restart',
            'node_drain',
            'node_force_drain',
            'node_activate',
            'node_retire'
        ));
-- +goose StatementEnd
