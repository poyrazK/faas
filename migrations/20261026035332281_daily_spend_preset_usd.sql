-- filename: 20261026035332281_daily_spend_preset_usd.sql
-- migration-compat: expand
--
-- ADR-1350 / ADR-1430: Gregale bills in US dollars. The billing-currency
-- switch (20261016100745318) retitled spend_eur_20 but missed
-- daily_spend_eur_1, so `gregale alerts preset list` on production-us
-- rc.294 showed "Daily usage cost exceeds €1" beside "Spend exceeds $20"
-- (Hunt 14 H14-69). daily_cost_cents is priced from pkg/api/limits.go in
-- USD. The preset keeps its name, which customers enable it by. Data only;
-- re-runnable.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
UPDATE alert_presets
   SET display_name = 'Daily usage cost exceeds $1',
       description = 'Fires when the app''s estimated raw RAM usage cost exceeds $1 during the current UTC day. This is a burn-rate signal; monthly included allowances are not subtracted.'
 WHERE name = 'daily_spend_eur_1';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
UPDATE alert_presets
   SET display_name = 'Daily usage cost exceeds €1',
       description = 'Fires when the app''s estimated raw RAM usage cost exceeds €1 during the current UTC day. This is a burn-rate signal; monthly included allowances are not subtracted.'
 WHERE name = 'daily_spend_eur_1';
-- +goose StatementEnd
