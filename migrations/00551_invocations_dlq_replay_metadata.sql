-- +goose Up
-- +goose StatementBegin
--
-- ADR-134 PR-C: DLQ replay trail on invocations.
--
-- Today's invocations table has a state value of 'dead_letter'
-- but no record of how a row got there or whether it has been
-- replayed before. The new queue DLQ replay handler
-- (POST /v1/apps/{slug}/queues/dead_letter/{invocationID}/replay)
-- resets a row to 'pending' with attempts=0; without this trail
-- the dashboard cannot distinguish "replayed 3 times, still
-- failing" from "first time it died".
--
-- Columns:
--   replayed_from_invocation_id UUID NULL
--     The dead-letter row this row was replayed from. NULL on
--     first entry. After a replay, the NEW row (created by the
--     drain's ClaimInvocationWithCap after the dead-letter row
--     transitions back to 'pending' and the drain picks it up)
--     carries the parent row's id here.
--     Implementation note: we use a self-reference instead of a
--     lineage table because the customer only ever cares about
--     the immediate parent (the dashboard renders "this row was
--     replayed from <row-id>" — not a chain).
--
--   last_replayed_at TIMESTAMPTZ NULL
--     When the most recent replay happened. NULL until the row
--     has been replayed at least once.
--
-- Index:
--   invocations_replayed_from_idx (account_id, last_replayed_at)
--     WHERE last_replayed_at IS NOT NULL
--     Used by the dashboard's "DLQ replay history" view
--     (GET /v1/apps/{slug}/queues/dead_letter/replays).
--
ALTER TABLE invocations
  ADD COLUMN IF NOT EXISTS replayed_from_invocation_id UUID NULL,
  ADD COLUMN IF NOT EXISTS last_replayed_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS invocations_replayed_from_idx
  ON invocations (account_id, last_replayed_at)
  WHERE last_replayed_at IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS invocations_replayed_from_idx;
ALTER TABLE invocations
  DROP COLUMN IF EXISTS last_replayed_at,
  DROP COLUMN IF EXISTS replayed_from_invocation_id;
-- +goose StatementEnd