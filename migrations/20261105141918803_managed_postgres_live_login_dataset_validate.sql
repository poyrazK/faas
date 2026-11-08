-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_postgres_live_logins
 VALIDATE CONSTRAINT managed_postgres_live_logins_data_resource_id_check,
 VALIDATE CONSTRAINT managed_postgres_live_logins_authorized_identity_id_check,
 VALIDATE CONSTRAINT managed_postgres_live_logins_dataset_pair_check;

-- +goose Down
SELECT 1;
