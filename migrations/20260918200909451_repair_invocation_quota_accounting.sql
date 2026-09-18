-- filename: 20260918200909451_repair_invocation_quota_accounting.sql

-- +goose Up
-- +goose StatementBegin
-- A dispatch owns an account_async_quota slot only when it was claimed by
-- ClaimInvocationWithCap. Persist that ownership on the row so every exit
-- from dispatching can release exactly the slot it acquired. The default is
-- false for compatibility with legacy/direct ClaimInvocation callers.
ALTER TABLE invocations
  ADD COLUMN IF NOT EXISTS quota_reserved BOOLEAN NOT NULL DEFAULT false;

-- Existing production dispatches predate the ownership marker. Treat every
-- currently dispatching row as reserved, then rebuild the denormalized
-- counters from the authoritative rows. This also repairs counters leaked by
-- dispatching -> pending retries and counters over-released by pending
-- cancellation/deadline transitions.
UPDATE invocations
   SET quota_reserved = (state = 'dispatching')
 WHERE quota_reserved IS DISTINCT FROM (state = 'dispatching');

UPDATE account_async_quota AS quota
   SET current_inflight = (
         SELECT count(*)
           FROM invocations AS invocation
          WHERE invocation.account_id = quota.account_id
            AND invocation.state = 'dispatching'
            AND invocation.quota_reserved
       ),
       updated_at = now();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE invocations
  DROP COLUMN IF EXISTS quota_reserved;
-- +goose StatementEnd
