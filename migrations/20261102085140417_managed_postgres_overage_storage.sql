-- migration-compat: expand
-- filename: 20261102085140417_managed_postgres_overage_storage.sql
--
-- ADR-20261031110037 amendment 2: managed PostgreSQL storage held past the
-- plan's pool is billed on the same delivery row as compute overage. The row
-- records the storage byte-seconds its cumulative charge billed. The previous
-- release's upsert does not name the column, so its rows keep the default;
-- the charge itself stays cumulative in delivered_millicents. The CHECK is
-- added NOT VALID: new writes are checked at once.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_overage_deliveries
    ADD COLUMN IF NOT EXISTS billed_storage_byte_seconds bigint NOT NULL DEFAULT 0;
ALTER TABLE managed_postgres_overage_deliveries
    ADD CONSTRAINT managed_postgres_overage_deliveries_billed_storage_check
    CHECK (billed_storage_byte_seconds >= 0) NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_overage_deliveries
    DROP CONSTRAINT IF EXISTS managed_postgres_overage_deliveries_billed_storage_check;
ALTER TABLE managed_postgres_overage_deliveries
    DROP COLUMN IF EXISTS billed_storage_byte_seconds;
-- +goose StatementEnd
