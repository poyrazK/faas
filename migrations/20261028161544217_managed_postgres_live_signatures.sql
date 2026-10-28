-- filename: 20261028161544217_managed_postgres_live_signatures.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR 20261028161502: apid signs each live key and login it seals (Ed25519
-- over the row's database, key ID, recipient and ciphertext), and livequeryd
-- verifies the signature before it opens the value. NULL is an unsigned row:
-- one written before signing, or re-sealed since, until the operator backfill
-- signs it. The length checks are added NOT VALID and validated by the next
-- migration, so adding them scans no table under an exclusive lock.
ALTER TABLE managed_postgres_live_keys ADD COLUMN IF NOT EXISTS sealed_secret_sig bytea;
ALTER TABLE managed_postgres_live_logins ADD COLUMN IF NOT EXISTS sealed_login_sig bytea;
ALTER TABLE managed_postgres_live_keys ADD CONSTRAINT managed_postgres_live_keys_sealed_secret_sig_check
    CHECK (sealed_secret_sig IS NULL OR octet_length(sealed_secret_sig) = 64) NOT VALID;
ALTER TABLE managed_postgres_live_logins ADD CONSTRAINT managed_postgres_live_logins_sealed_login_sig_check
    CHECK (sealed_login_sig IS NULL OR octet_length(sealed_login_sig) = 64) NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE managed_postgres_live_logins DROP COLUMN IF EXISTS sealed_login_sig;
ALTER TABLE managed_postgres_live_keys DROP COLUMN IF EXISTS sealed_secret_sig;
-- +goose StatementEnd
