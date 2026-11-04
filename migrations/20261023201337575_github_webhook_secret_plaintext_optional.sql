-- filename: 20261023201337575_github_webhook_secret_plaintext_optional.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR 20261022111812: GitHub webhook secrets stop being written in plaintext.
-- From this release apid writes only secret_sealed; githubd has preferred it
-- since the release that added it, and still falls back to secret_value for
-- rows written before that. The previous release keeps writing both. A later
-- release drops secret_value, once `gregalectl seal-domain migrate --domain
-- source` has given every row a sealed copy.
ALTER TABLE github_webhook_secrets ALTER COLUMN secret_value DROP NOT NULL;
-- Every row keeps at least one copy of its secret. New writes are checked at
-- once; a later migration validates the existing rows, which all hold
-- secret_value today.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'github_webhook_secrets_secret_present_chk'
           AND conrelid = 'github_webhook_secrets'::regclass
    ) THEN
        ALTER TABLE github_webhook_secrets
            ADD CONSTRAINT github_webhook_secrets_secret_present_chk
            CHECK (secret_value IS NOT NULL OR secret_sealed IS NOT NULL) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

COMMENT ON COLUMN github_webhook_secrets.secret_value IS
    'Plaintext webhook secret, no longer written (ADR 20261022111812). Read only as a fallback for rows without secret_sealed; dropped in a later release.';

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- secret_value stays nullable: rows written since the Up have no plaintext,
-- so NOT NULL cannot come back.
ALTER TABLE github_webhook_secrets DROP CONSTRAINT IF EXISTS github_webhook_secrets_secret_present_chk;
COMMENT ON COLUMN github_webhook_secrets.secret_value IS NULL;
