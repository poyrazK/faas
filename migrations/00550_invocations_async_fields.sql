-- +goose Up
-- +goose StatementBegin
--
-- ADR-134 PR-B: per-row async-job semantics on invocations.
--
-- Adds three optional columns so each invocation can carry its own
-- deadline, retry policy override, and result-retention horizon. The
-- pkg/sched drain already does CAS lease + retry/deadline via global
-- plan-level defaults (MaxAsyncInvocationsPerAccount,
-- MaxAsyncInvocationDeadlineSeconds). These columns make the same
-- shape per-row so high-value async jobs (the customer's "must
-- finish by 09:00" cron) can override the plan default without a
-- separate API surface.
--
-- All three columns are NULLABLE and additive — no existing row is
-- affected, and apid's existing INSERT path is unchanged when the
-- caller omits the new fields. The drain's hot path (ListDueInvocations
-- + ClaimInvocation) is also unchanged; PR-B's drain hook reads the
-- new columns only when they are non-NULL.
--
-- Indexes:
--   invocations_app_deadline_idx
--     Used by the deadline-breach reaper. The reaper scans rows in
--     (pending|dispatching) and (deadline_at <= now()), so the
--     partial index keeps the scan bounded by the rare rows that
--     carry a deadline (today: zero — every adoption flips the cost
--     curve from O(pending) to O(deadline_set)).
--   invocations_acct_retention_idx
--     Used by the retention reaper. The reaper deletes rows in
--     (state IN ('completed','failed','dead_letter','cancelled'))
--     AND result_retention_until <= now(). Partial because most
--     invocations carry NULL retention (reaper accepts the default
--     horizon from MaxAsyncResultRetentionSeconds) — only the
--     explicit override rows land in this index.
--
-- ResultRetentionUntil: timestamptz NULL. NULL means "use plan
-- default retention". Non-NULL means "delete at this absolute
-- timestamp, regardless of plan default".
--
ALTER TABLE invocations
  ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ NULL,
  ADD COLUMN IF NOT EXISTS retry_policy JSONB NULL,
  ADD COLUMN IF NOT EXISTS result_retention_until TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS invocations_app_deadline_idx
  ON invocations (app_id, deadline_at)
  WHERE state IN ('pending', 'dispatching') AND deadline_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS invocations_acct_retention_idx
  ON invocations (account_id, result_retention_until)
  WHERE result_retention_until IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_acct_retention_idx;
DROP INDEX IF EXISTS invocations_app_deadline_idx;
ALTER TABLE invocations
  DROP COLUMN IF EXISTS result_retention_until,
  DROP COLUMN IF EXISTS retry_policy,
  DROP COLUMN IF EXISTS deadline_at;
-- +goose StatementEnd