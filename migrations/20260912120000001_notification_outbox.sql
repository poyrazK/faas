-- filename: 20260912120000001_notification_outbox.sql

-- +goose Up
-- +goose StatementBegin
-- Durable handoff queue for the deploy pipeline. pg_notify remains the
-- low-latency wakeup, but a LISTEN gap or daemon restart no longer strands a
-- deployment in an intermediate state.
CREATE TABLE IF NOT EXISTS notification_outbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    channel      text NOT NULL,
    payload      text NOT NULL,
    state        text NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending', 'processing', 'delivered', 'dead_letter')),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_by   text,
    claimed_at   timestamptz,
    lease_until  timestamptz,
    delivered_at timestamptz,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS notification_outbox_claim_idx
    ON notification_outbox (channel, available_at, id)
    WHERE state IN ('pending', 'processing');

CREATE INDEX IF NOT EXISTS notification_outbox_retention_idx
    ON notification_outbox (state, created_at)
    WHERE state IN ('delivered', 'dead_letter');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_outbox;
-- +goose StatementEnd
