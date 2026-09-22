-- filename: 20260922192046350_event_delivery_lookup.sql

-- +goose Up
-- +goose StatementBegin
-- Event-triggered invocations are identified by the canonical event id
-- header. Keep the app-scoped delivery inspection query index-backed without
-- affecting ordinary async invocation history.
CREATE INDEX IF NOT EXISTS invocations_event_delivery_idx
  ON invocations (app_id, created_at DESC, id DESC)
  WHERE source = 'async_invoke' AND headers ? 'x-gregale-event-id';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_event_delivery_idx;
-- +goose StatementEnd
