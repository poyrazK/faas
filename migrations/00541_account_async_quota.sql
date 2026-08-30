-- +goose Up
-- +goose StatementBegin
--
-- ADR-134 PR-B: per-account async-job concurrency counter.
--
-- The pre-PR-B platform enforced per-app caps (MaxQueueDepth on the
-- queues endpoint) but no per-account cap, so a Pro account with 25
-- apps could land 25 × MaxQueueDepth = thousands of in-flight async
-- invocations. The new MaxAsyncInvocationsPerAccount limit closes
-- that gap by counting inflight (state='dispatching') invocations
-- across every app owned by the account.
--
-- Schema choice: COUNTER table, not live-count.
--
--   - Live-count means `SELECT count(*) FROM invocations WHERE
--     account_id=$1 AND state='dispatching'` on every Claim. At
--     Scale plan (100k inflight cap) and high claim rate this is
--     O(rows), expensive, and racy: two claimers read the same count
--     and both pass the check.
--   - Counter table means an atomic UPDATE inside the claim
--     transaction:
--
--         UPDATE account_async_quota
--            SET current_inflight = current_inflight + 1
--          WHERE account_id = $1
--            AND current_inflight < max_inflight
--        RETURNING current_inflight
--
--     The UPDATE is the cap check. 0 rows returned = cap hit. Same
--     transaction as the invocations UPDATE so the increment is
--     atomic with the state transition; the drain cannot exceed the
--     cap even under concurrent claim.
--
-- Row shape:
--   account_id     UUID PRIMARY KEY — the account.
--   max_inflight   INT NOT NULL CHECK >= 0 — the plan's cap
--                   (MaxAsyncInvocationsPerAccount). Mirrored on
--                   the row so apid's plan-change path can
--                   UPDATE the cap in one query; no need to chase
--                   the plan in pkg/api.Limits at runtime.
--   current_inflight INT NOT NULL DEFAULT 0 CHECK >= 0 — the live
--                   counter. Decremented in the same transaction
--                   that completes / fails / cancels an invocation,
--                   so the counter never goes negative in practice.
--                   The CHECK is defense-in-depth for a logic bug.
--   updated_at     TIMESTAMPTZ NOT NULL DEFAULT now() — bumped on
--                   every UPDATE for ops debugging; cheap to write.
--
-- The drain lazily INSERTs the row on first claim (ON CONFLICT
-- DO UPDATE) so apid does not need a "first-deploy" provisioning
-- step. The plan's max_inflight is read from pkg/api.Limits at that
-- point, so plan changes are picked up at next INSERT.
--
CREATE TABLE IF NOT EXISTS account_async_quota (
  account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  max_inflight INT NOT NULL CHECK (max_inflight >= 0),
  current_inflight INT NOT NULL DEFAULT 0 CHECK (current_inflight >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS account_async_quota;
-- +goose StatementEnd