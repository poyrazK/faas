-- +goose Up
-- +goose StatementBegin
-- EPIC #1278 / Workstream A: retain the terminal callback intent on the
-- invocation row so completion and failure delivery survives apid restarts.
-- Destinations reuse the existing app_webhooks durable delivery ledger;
-- foreign keys keep a deleted subscription from leaving a dangling target.
ALTER TABLE invocations
  ADD COLUMN IF NOT EXISTS on_success_destination_id uuid NULL
    REFERENCES app_webhooks(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS on_failure_destination_id uuid NULL
    REFERENCES app_webhooks(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS invocations_success_destination_idx
  ON invocations (on_success_destination_id)
  WHERE on_success_destination_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS invocations_failure_destination_idx
  ON invocations (on_failure_destination_id)
  WHERE on_failure_destination_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: terminal callback intent is part of the durable invocation
-- contract. Removing it would silently discard customer-configured delivery
-- semantics for rows already in flight.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
