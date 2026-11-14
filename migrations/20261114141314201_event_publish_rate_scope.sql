-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Event-driven hunt #16: the per-account event publish rate
-- (api.EventPublishLimits, ADR-20261026140000) was kept per process, so each
-- apid replica and each vmmd node admitted the plan rate on its own. Every
-- publisher now also charges one shared counter per account in this scope.
-- NOT VALID then VALIDATE keeps the swap to a brief lock; existing rows
-- already satisfy the wider check.
-- +goose StatementBegin
ALTER TABLE pg_ratelimit_counters
    DROP CONSTRAINT IF EXISTS pg_ratelimit_counters_scope_check;
ALTER TABLE pg_ratelimit_counters
    ADD CONSTRAINT pg_ratelimit_counters_scope_check
    CHECK (scope IN ('app', 'account', 'rule', 'preauth', 'event_publish')) NOT VALID;
ALTER TABLE pg_ratelimit_counters
    VALIDATE CONSTRAINT pg_ratelimit_counters_scope_check;
-- +goose StatementEnd

-- +goose Down
-- Counters can still exist, so keep the widened constraint on downgrade.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
