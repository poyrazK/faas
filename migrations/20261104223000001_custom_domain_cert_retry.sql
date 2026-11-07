-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains ADD COLUMN IF NOT EXISTS cert_retry_at timestamptz;
-- +goose StatementBegin
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conrelid = 'custom_domains'::regclass
                     AND conname = 'custom_domains_cert_retry_at_check') THEN
        ALTER TABLE custom_domains ADD CONSTRAINT custom_domains_cert_retry_at_check
            CHECK (cert_retry_at IS NULL OR isfinite(cert_retry_at)) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains DROP COLUMN IF EXISTS cert_retry_at;
