-- +goose Up
-- +goose StatementBegin
-- Account dashboard history for async HTTP invocations. Keeping this partial
-- index source-scoped avoids growing a second all-source history index.
CREATE INDEX IF NOT EXISTS invocations_async_account_history_idx
  ON invocations (account_id, created_at DESC, id DESC)
  WHERE source = 'async_invoke';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_async_account_history_idx;
-- +goose StatementEnd
