-- filename: 20261028161546331_managed_postgres_live_signatures_validate.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR 20261028161502: validate the signature length checks the previous
-- migration added NOT VALID. VALIDATE takes a SHARE UPDATE EXCLUSIVE lock,
-- so reads and writes continue while it scans.
ALTER TABLE managed_postgres_live_keys VALIDATE CONSTRAINT managed_postgres_live_keys_sealed_secret_sig_check;
ALTER TABLE managed_postgres_live_logins VALIDATE CONSTRAINT managed_postgres_live_logins_sealed_login_sig_check;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
