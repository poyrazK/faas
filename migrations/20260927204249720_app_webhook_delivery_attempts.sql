-- +goose Up
-- A replay re-arms the seven-attempt budget on the same delivery row. Keep
-- prior rounds distinguishable while preserving each completed attempt.
ALTER TABLE app_webhook_deliveries
    ADD COLUMN IF NOT EXISTS replay_generation integer NOT NULL DEFAULT 0
        CHECK (replay_generation >= 0);

CREATE TABLE IF NOT EXISTS app_webhook_delivery_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_id uuid NOT NULL REFERENCES app_webhook_deliveries(id) ON DELETE CASCADE,
    replay_generation integer NOT NULL CHECK (replay_generation >= 0),
    attempt_number integer NOT NULL CHECK (attempt_number BETWEEN 1 AND 8),
    outcome text NOT NULL CHECK (outcome IN ('succeeded', 'retrying', 'dead')),
    response_code integer NOT NULL DEFAULT 0 CHECK (response_code BETWEEN 0 AND 999),
    error text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    next_attempt_at timestamptz,
    CONSTRAINT app_webhook_delivery_attempts_time_chk CHECK (finished_at >= started_at),
    CONSTRAINT app_webhook_delivery_attempts_unique UNIQUE (delivery_id, replay_generation, attempt_number)
);

-- +goose Down
-- Forward-only: dropping the ledger would erase customer-visible attempt history.
SELECT 1;
