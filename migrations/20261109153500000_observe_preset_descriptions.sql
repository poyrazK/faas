-- migration-compat: expand
-- filename: 20261109153500000_observe_preset_descriptions.sql

-- +goose Up
-- ADR-20261109153000: preset descriptions say what their metrics count. The
-- cold-start preset counts cold boots, not snapshot wakes, and the spend
-- preset reads compute, egress and object-storage overage.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
UPDATE alert_presets
   SET description = 'Fires when more than 10 percent of requests over the last hour needed a cold boot (no usable snapshot), with at least 10 requests in the hour. A sustained rate usually means snapshots are missing or stale after a deploy.'
 WHERE name = 'cold_start_10pct';
UPDATE alert_presets
   SET description = 'Fires when the account''s month-to-date charges beyond its plan exceed $20: compute overage, elected egress overage and object storage, the overage the billing page shows.'
 WHERE name = 'spend_eur_20';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
UPDATE alert_presets
   SET description = 'Fires when more than 10 percent of invocations over the last hour are cold starts (parked-snapshot wake). Indicates traffic is bouncing the wake-queue cap.'
 WHERE name = 'cold_start_10pct';
UPDATE alert_presets
   SET description = 'Fires when the account''s month-to-date spend exceeds $20. Backed by the meterd MTD billing aggregator (plan RAM + 8 MB per running second; rate from pkg/api/limits.go).'
 WHERE name = 'spend_eur_20';
-- +goose StatementEnd
