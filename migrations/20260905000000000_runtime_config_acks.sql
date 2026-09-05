-- filename: 20260905000000000_runtime_config_acks.sql
-- ADR-132 follow-up: durable per-daemon convergence acknowledgements.
-- The desired/effective row remains the control-plane aggregate; this table
-- records what each daemon/node has actually observed for a version.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS runtime_config_acks (
    config_key      text NOT NULL,
    scope           text NOT NULL DEFAULT 'global'
                    CHECK (scope IN ('global', 'control_plane', 'daemon', 'node')),
    scope_id        text NOT NULL DEFAULT '',
    consumer        text NOT NULL,
    node_id         text NOT NULL DEFAULT '',
    config_version  bigint NOT NULL CHECK (config_version > 0),
    status          text NOT NULL
                    CHECK (status IN ('applied', 'failed')),
    effective_value jsonb NULL,
    error           text NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    applied_at      timestamptz NULL,
    PRIMARY KEY (config_key, scope, scope_id, consumer, node_id)
);

CREATE INDEX IF NOT EXISTS runtime_config_acks_lookup_idx
    ON runtime_config_acks (config_key, scope, scope_id, config_version);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS runtime_config_acks_lookup_idx;
DROP TABLE IF EXISTS runtime_config_acks;
-- +goose StatementEnd
