-- filename: 20261017102210208_operator_database_restart_intent.sql

-- ADR-1027: operators restart a native database in place through the
-- durable operator-intent queue, to move it onto its node's current base
-- (a new PostgreSQL minor, guest-init or launch), or switch a
-- production-class database over to its synchronized standby. apid
-- records the request; schedd's database scheduler parks the database and
-- wakes it again, or stops its primary cleanly and promotes the standby.
-- target_id is the database id.
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
            'database_move',
            'database_restart',
            'database_switchover'
        ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM operator_intents WHERE kind IN ('database_restart', 'database_switchover');

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
