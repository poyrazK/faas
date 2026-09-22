-- filename: 20260922123607174_invocation_trace_lookup_all_sources.sql

-- +goose Up
-- +goose StatementBegin
-- Async, delayed, cron, replay, and event-fanout invocations now carry the
-- same platform-owned trace header as queue rows. Keep the account predicate
-- first so the tenant-scoped lookup remains selective, while retaining the
-- older queue-only index for mixed-version boxes during rollout.
CREATE INDEX IF NOT EXISTS invocations_account_trace_all_idx
  ON invocations (account_id, ((headers->>'X-Gregale-Trace-Id')), created_at DESC)
  WHERE headers ? 'X-Gregale-Trace-Id';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_account_trace_all_idx;
-- +goose StatementEnd
