-- filename: 20260928175459877_platform_tenant_account_created_id_index.sql

-- +goose Up
-- +goose StatementBegin
-- The account registry uses a stable newest-first keyset cursor. Keep the
-- account predicate and the (created_at, id) ordering on one index so a
-- tenant platform can page large registries without repeatedly sorting them.
CREATE INDEX IF NOT EXISTS platform_tenants_account_created_id_idx
  ON platform_tenants (account_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS platform_tenants_account_created_id_idx;
-- +goose StatementEnd
