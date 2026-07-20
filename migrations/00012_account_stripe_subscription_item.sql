-- +goose Up
-- +goose StatementBegin

-- Issue #52 (M7). Records the per-account Stripe subscription item ID
-- (sub_item_…) so meterd's hourly push can target the right metered
-- usage record without a per-account in-memory map (pkg/stripex is a
-- single node — no Redis). NULL by default so existing rows on a live
-- deploy don't have to migrate; products.go's EnsureCustomer stamps this
-- on the first successful subscription webhook.
--
-- Idempotent (IF NOT EXISTS) so the migration can be re-run during
-- local development.

alter table accounts
  add column if not exists stripe_subscription_item text;

-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
alter table accounts drop column if exists stripe_subscription_item;
-- +goose StatementEnd
