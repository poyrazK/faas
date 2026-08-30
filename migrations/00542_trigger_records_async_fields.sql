-- +goose Up
-- +goose StatementBegin
--
-- ADR-134 PR-C: per-row async-job semantics on trigger_records.
--
-- Mirrors 00550_invocations_async_fields.sql — three nullable
-- columns so each trigger_record can carry its own deadline,
-- retry policy override, and result-retention horizon. The
-- pkg/sched dispatch_triggers.go drain (the trigger_records
-- counterpart to pkg/sched/drain.go) already does CAS lease +
-- retry/deadline via global trigger-level defaults; per-row
-- overrides flow through dispatch.RetryPolicy /
-- dispatch.DeadlinePolicy (PR-A's shared contract).
--
-- All three columns are NULLABLE and additive — no existing row
-- is affected, and the trigger_dispatcher path is unchanged
-- when the caller omits the new fields.
--
-- Index:
--   trigger_records_trigger_deadline_idx
--     Used by the deadline-breach reaper. The reaper scans rows
--     in (pending|claimed|retry) and (deadline_at <= now()), so
--     the partial index keeps the scan bounded by the rare rows
--     that carry a deadline.
--
-- ResultRetentionUntil: NULL means "use plan default retention".
--
-- PR-C fixup (CI #1185): the index column is trigger_id, NOT
-- app_id — trigger_records has no app_id column. The FK chain is
-- trigger_records.trigger_id → triggers.id → triggers.app_id →
-- apps.id; per-trigger scans in dispatch_triggers.go already
-- filter on trigger_id, so the partial index matches the access
-- pattern.
--
ALTER TABLE trigger_records
  ADD COLUMN IF NOT EXISTS deadline_at TIMESTAMPTZ NULL,
  ADD COLUMN IF NOT EXISTS retry_policy JSONB NULL,
  ADD COLUMN IF NOT EXISTS result_retention_until TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS trigger_records_trigger_deadline_idx
  ON trigger_records (trigger_id, deadline_at)
  WHERE state IN ('pending', 'claimed', 'retry') AND deadline_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS trigger_records_trigger_deadline_idx;
ALTER TABLE trigger_records
  DROP COLUMN IF EXISTS result_retention_until,
  DROP COLUMN IF EXISTS retry_policy,
  DROP COLUMN IF EXISTS deadline_at;
-- +goose StatementEnd