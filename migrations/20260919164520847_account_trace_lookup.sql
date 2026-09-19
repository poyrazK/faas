-- filename: 20260919164520847_account_trace_lookup.sql

-- +goose Up
-- +goose StatementBegin
-- Durable lookup for platform-created queue trace links. Queue messages keep
-- the canonical trace id in the invocation header envelope; this expression
-- index makes account-scoped trace queries index-backed without exposing
-- payloads or customer-controlled headers to the lookup surface.
CREATE INDEX IF NOT EXISTS invocations_account_trace_idx
  ON invocations (account_id, ((headers->>'X-Gregale-Trace-Id')), created_at DESC)
  WHERE source = 'queue' AND headers ? 'X-Gregale-Trace-Id';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_account_trace_idx;
-- +goose StatementEnd
