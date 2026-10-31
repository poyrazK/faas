-- filename: 20261031153318512_usage_quiet_paused_mb_seconds.sql
-- migration-compat: expand
-- ADR-20261023171905 rollout step 3: the customer-visible record of quiet
-- pause. meterd writes, per instance-minute, the MB-seconds it did not bill
-- because the pause ledger proved the VM quiet-paused (quiet_pause_unbilled
-- on, and the account released by the quiet_pause_unbilled flag); 0
-- everywhere else. The account usage view sums it for the month, so the raw
-- ledger only needs to outlive meterd's catch-up window. A constant default
-- adds the column without rewriting the table; a previous release neither
-- writes nor reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_minutes ADD COLUMN IF NOT EXISTS paused_mb_seconds bigint NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_minutes DROP COLUMN IF EXISTS paused_mb_seconds;
-- +goose StatementEnd
