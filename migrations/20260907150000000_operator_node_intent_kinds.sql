-- filename: 20260907150000000_operator_node_intent_kinds.sql
-- +goose Up
-- +goose StatementBegin
-- Provider operations: route compute-node lifecycle changes through the
-- durable operator_intents queue. schedd owns dispatch because it already
-- owns node recovery and instance migration. Keeping the vocabulary closed
-- makes an unimplemented operation fail at INSERT instead of becoming an
-- arbitrary command channel.
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
            'node_activate'
        ));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE operator_intents
    DROP CONSTRAINT IF EXISTS operator_intents_kind_check;

ALTER TABLE operator_intents
    ADD CONSTRAINT operator_intents_kind_check
        CHECK (kind IN ('force_park','force_cold_boot','force_restart'));
-- +goose StatementEnd
