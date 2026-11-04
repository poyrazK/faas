-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains ADD COLUMN cert_retry_at timestamptz;
ALTER TABLE custom_domains ADD CONSTRAINT custom_domains_cert_retry_at_check
    CHECK (cert_retry_at IS NULL OR isfinite(cert_retry_at)) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains DROP COLUMN IF EXISTS cert_retry_at;
