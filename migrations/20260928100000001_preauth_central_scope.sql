-- +goose Up
-- +goose StatementBegin
-- Shared pre-auth route budgets use bounded UUID shards in their own scope.
-- Separating this scope lets the gateway prune idle source counters without
-- changing the retention of existing app, account, or edge-rule counters.
ALTER TABLE pg_ratelimit_counters
    DROP CONSTRAINT IF EXISTS pg_ratelimit_counters_scope_check;
ALTER TABLE pg_ratelimit_counters
    ADD CONSTRAINT pg_ratelimit_counters_scope_check
    CHECK (scope IN ('app', 'account', 'rule', 'preauth'));
CREATE INDEX IF NOT EXISTS pg_ratelimit_counters_preauth_refill_idx
    ON pg_ratelimit_counters (last_refill)
    WHERE scope = 'preauth';
-- +goose StatementEnd

-- +goose Down
-- Counters can still exist, so keep the widened constraint on downgrade.
-- +goose StatementBegin
DROP INDEX IF EXISTS pg_ratelimit_counters_preauth_refill_idx;
SELECT 1;
-- +goose StatementEnd
