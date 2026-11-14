-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE referral_links ADD COLUMN IF NOT EXISTS share_code text;
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='referral_links'::regclass AND conname='referral_links_share_code_check') THEN
  ALTER TABLE referral_links ADD CONSTRAINT referral_links_share_code_check
 CHECK (share_code IS NULL OR share_code ~ '^[A-Z0-9]{6}$') NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE referral_links DROP COLUMN IF EXISTS share_code;
