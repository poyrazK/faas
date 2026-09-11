-- Terminal compute-node retirement. Unlike unavailable, retired nodes are not
-- eligible for heartbeat recovery or operator re-enrollment. The lifecycle
-- transition is dispatched through schedd's durable operator intent queue.
-- +goose Up
-- +goose StatementBegin
ALTER TYPE compute_node_lifecycle ADD VALUE IF NOT EXISTS 'retired';

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

-- +goose Down
-- PostgreSQL enum values cannot be removed safely while rows may reference
-- them. Retirement is therefore an append-only schema capability.
SELECT 1;
