-- +goose Up
-- Durable idempotency ledger for gateway usage stream ACKs. Meterd records an
-- event and applies its request/byte counters to usage_minutes in one
-- transaction; only then may it acknowledge the gateway replay frame.
CREATE TABLE meter_gateway_usage_events (
    node_id uuid NOT NULL,
    event_id uuid NOT NULL,
    instance_id uuid NOT NULL,
    minute timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (node_id, event_id)
);

CREATE INDEX meter_gateway_usage_events_recorded_at_idx
    ON meter_gateway_usage_events (recorded_at);

-- +goose Down
DROP TABLE IF EXISTS meter_gateway_usage_events;
