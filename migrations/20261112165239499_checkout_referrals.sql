-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE referral_attributions ADD COLUMN attribution_source text NOT NULL DEFAULT 'signup';
ALTER TABLE referral_attributions ADD CONSTRAINT referral_attributions_source_check
 CHECK (attribution_source IN ('signup','checkout')) NOT VALID;
ALTER TABLE referral_attributions VALIDATE CONSTRAINT referral_attributions_source_check;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
ALTER TABLE referral_attributions DROP COLUMN attribution_source;
RESET lock_timeout;
