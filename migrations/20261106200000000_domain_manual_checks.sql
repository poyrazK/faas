-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains ADD COLUMN IF NOT EXISTS verification_manual_check_at timestamptz;
ALTER TABLE custom_domains ADD CONSTRAINT custom_domains_manual_check_at_check
    CHECK (verification_manual_check_at IS NULL OR isfinite(verification_manual_check_at)) NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE custom_domains DROP COLUMN IF EXISTS verification_manual_check_at;
