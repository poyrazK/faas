-- filename: 20261028161544217_managed_postgres_live_signatures.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR 20261028161502: apid signs each live key and login it seals (Ed25519
-- over the row's database, key ID, recipient and ciphertext), and livequeryd
-- verifies the signature before it opens the value. NULL is an unsigned row:
-- one written before signing, or re-sealed since, until the operator backfill
-- signs it. Additive and replay-safe. The length checks are added NOT VALID,
-- so adding them scans no table under an exclusive lock;
-- 20261028161546331 validates them.
ALTER TABLE managed_postgres_live_keys ADD COLUMN IF NOT EXISTS sealed_secret_sig bytea;
ALTER TABLE managed_postgres_live_logins ADD COLUMN IF NOT EXISTS sealed_login_sig bytea;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'managed_postgres_live_keys_sealed_secret_sig_check' AND conrelid = 'managed_postgres_live_keys'::regclass) THEN
        ALTER TABLE managed_postgres_live_keys ADD CONSTRAINT managed_postgres_live_keys_sealed_secret_sig_check
            CHECK (sealed_secret_sig IS NULL OR octet_length(sealed_secret_sig) = 64) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'managed_postgres_live_logins_sealed_login_sig_check' AND conrelid = 'managed_postgres_live_logins'::regclass) THEN
        ALTER TABLE managed_postgres_live_logins ADD CONSTRAINT managed_postgres_live_logins_sealed_login_sig_check
            CHECK (sealed_login_sig IS NULL OR octet_length(sealed_login_sig) = 64) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE managed_postgres_live_logins
    DROP CONSTRAINT IF EXISTS managed_postgres_live_logins_sealed_login_sig_check,
    DROP COLUMN IF EXISTS sealed_login_sig;
ALTER TABLE managed_postgres_live_keys
    DROP CONSTRAINT IF EXISTS managed_postgres_live_keys_sealed_secret_sig_check,
    DROP COLUMN IF EXISTS sealed_secret_sig;
-- +goose StatementEnd
