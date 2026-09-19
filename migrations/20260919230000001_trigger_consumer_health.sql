-- +goose Up
-- Durable last-known health for queue/event-source consumers. The scheduler
-- updates this row after every poll; apid reads it for binding status without
-- reaching into schedd process memory.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS trigger_consumer_health (
    trigger_id        UUID PRIMARY KEY REFERENCES triggers(id) ON DELETE CASCADE,
    last_poll_at      TIMESTAMPTZ NOT NULL,
    last_success_at   TIMESTAMPTZ NULL,
    last_error_at     TIMESTAMPTZ NULL,
    last_error        TEXT NULL,
    lag_messages      BIGINT NULL CHECK (lag_messages >= 0),
    lag_age_seconds   DOUBLE PRECISION NULL CHECK (lag_age_seconds >= 0),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS trigger_consumer_health;
-- +goose StatementEnd
