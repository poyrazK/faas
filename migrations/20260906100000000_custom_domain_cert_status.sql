-- F1 / issue #1397: persist the legacy custom-domain TLS lifecycle.
-- The DNS poller is the writer for these observations; API clients read
-- the row without having to perform a live port-443 probe.
-- +goose Up
ALTER TABLE custom_domains
    ADD COLUMN IF NOT EXISTS cert_status text NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS cert_expires_at timestamptz,
    ADD COLUMN IF NOT EXISTS cert_last_error text,
    ADD COLUMN IF NOT EXISTS dns_last_checked_at timestamptz;

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'custom_domains_cert_status_chk'
    ) THEN
        ALTER TABLE custom_domains
            ADD CONSTRAINT custom_domains_cert_status_chk
            CHECK (cert_status IN ('pending', 'issued', 'renewing', 'failed'));
    END IF;
END $$;
-- +goose StatementEnd

CREATE INDEX IF NOT EXISTS custom_domains_cert_expiry_idx
    ON custom_domains (cert_expires_at)
    WHERE cert_status IN ('issued', 'renewing');

-- +goose Down
DROP INDEX IF EXISTS custom_domains_cert_expiry_idx;
ALTER TABLE custom_domains
    DROP CONSTRAINT IF EXISTS custom_domains_cert_status_chk,
    DROP COLUMN IF EXISTS dns_last_checked_at,
    DROP COLUMN IF EXISTS cert_last_error,
    DROP COLUMN IF EXISTS cert_expires_at,
    DROP COLUMN IF EXISTS cert_status;
