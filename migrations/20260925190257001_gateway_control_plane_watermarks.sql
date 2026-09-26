-- +goose Up
-- A serving gateway publishes the highest committed control-plane change it
-- has applied. boot_id prevents a restarted process from inheriting an old
-- process's observed position when the ledger has since been pruned.
CREATE TABLE IF NOT EXISTS gateway_control_plane_watermarks (
    node_name       text PRIMARY KEY CHECK (node_name <> ''),
    boot_id         uuid NOT NULL,
    last_change_id  bigint NOT NULL CHECK (last_change_id >= 0),
    observed_at     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS gateway_control_plane_watermarks;
