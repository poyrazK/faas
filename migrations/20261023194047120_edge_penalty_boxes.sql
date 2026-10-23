-- filename: 20261023194047120_edge_penalty_boxes.sql
--
-- ADR-20261022105846 amendment 1: penalty boxes shared between gateways.
-- Each gatewayd-internal appends the boxes it makes; every gateway reads
-- the boxes created since its last read, on an edge_penalty_box
-- notification and every 15 s, and loads the unexpired ones at start, so a
-- box holds on every node and survives a restart. The table is a log, not
-- a set: a client boxed twice has two rows and gateways keep the later end.
-- Rows are pruned an hour after their box ends. Only the client's address
-- (IPv4) or /64 (IPv6) is stored.
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS edge_penalty_boxes (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    app_id      uuid NOT NULL,
    rule_id     uuid NOT NULL,
    client      cidr NOT NULL CHECK (
                    (family(client) = 4 AND masklen(client) = 32)
                    OR (family(client) = 6 AND masklen(client) = 64)),
    boxed_until timestamptz NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS edge_penalty_boxes_created_idx
    ON edge_penalty_boxes (created_at);

CREATE INDEX IF NOT EXISTS edge_penalty_boxes_until_idx
    ON edge_penalty_boxes (boxed_until);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS edge_penalty_boxes;
-- +goose StatementEnd
