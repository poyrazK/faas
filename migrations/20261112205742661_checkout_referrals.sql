-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE referral_attributions ADD COLUMN IF NOT EXISTS attribution_source text NOT NULL DEFAULT 'signup';
-- +goose StatementBegin
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='referral_attributions'::regclass AND conname='referral_attributions_source_check') THEN
  ALTER TABLE referral_attributions ADD CONSTRAINT referral_attributions_source_check
 CHECK (attribution_source IN ('signup','checkout')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE referral_attributions VALIDATE CONSTRAINT referral_attributions_source_check;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE referral_attributions DROP COLUMN IF EXISTS attribution_source;
RESET lock_timeout;
