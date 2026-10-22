-- filename: 20261022090211159_seal_domains.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Seal domains (ADR 20261022111812). One non-secret probe per domain, sealed
-- to the domain recipient alone: a node proves it holds a domain identity by
-- opening its probe, as node join does for the fleet identity (ADR-178).
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS seal_domain_probe (
    domain      text PRIMARY KEY CHECK (domain ~ '^[a-z][a-z-]{0,31}$'),
    recipient   text NOT NULL CHECK (recipient ~ '^age1[0-9a-z]+$'),
    sealed_blob bytea NOT NULL CHECK (octet_length(sealed_blob) BETWEEN 1 AND 4096),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

COMMENT ON TABLE seal_domain_probe IS
    'One non-secret secretbox probe per seal domain, sealed to the domain recipient alone (ADR 20261022111812).';

-- GitHub webhook HMAC secrets were stored in plaintext. secret_sealed holds
-- the copy githubd opens; secret_value stays written for the release that
-- reads only it and is dropped in a later release.
ALTER TABLE github_webhook_secrets ADD COLUMN IF NOT EXISTS secret_sealed bytea;
-- Guarded so a replay over an applied schema is a no-op.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'github_webhook_secrets_secret_sealed_chk'
           AND conrelid = 'github_webhook_secrets'::regclass
    ) THEN
        ALTER TABLE github_webhook_secrets
            ADD CONSTRAINT github_webhook_secrets_secret_sealed_chk
            CHECK (secret_sealed IS NULL OR octet_length(secret_sealed) BETWEEN 1 AND 4096) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN github_webhook_secrets.secret_sealed IS
    'The webhook secret sealed to the source domain write recipients (ADR 20261022111812); githubd prefers it over secret_value.';

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE github_webhook_secrets DROP CONSTRAINT IF EXISTS github_webhook_secrets_secret_sealed_chk;
ALTER TABLE github_webhook_secrets DROP COLUMN IF EXISTS secret_sealed;
DROP TABLE IF EXISTS seal_domain_probe;
