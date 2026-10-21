-- filename: 20261014101517148_outbound_ai_settlements.sql
-- ADR-1120: AI gateway settlement keys. A call's settlement inserts its
-- admission lease ID, so a retried settlement whose first attempt committed
-- changes nothing. outboundd prunes keys older than an hour. Additive and
-- replay-safe.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS outbound_ai_settlements (
    lease_id       uuid PRIMARY KEY,
    integration_id uuid NOT NULL REFERENCES outbound_integrations(id) ON DELETE CASCADE,
    settled_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbound_ai_settlements_settled_at_idx ON outbound_ai_settlements (settled_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_settlements;
-- +goose StatementEnd
