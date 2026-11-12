-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE referral_links ADD COLUMN share_code text;
ALTER TABLE referral_links ADD CONSTRAINT referral_links_share_code_check
 CHECK (share_code IS NULL OR share_code ~ '^[A-Z0-9]{6}$') NOT VALID;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE referral_links DROP COLUMN IF EXISTS share_code;
