-- migration-compat: expand
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
SET statement_timeout = 0;
CREATE UNIQUE INDEX CONCURRENTLY referral_links_share_code_unique
 ON referral_links(share_code) WHERE share_code IS NOT NULL;
ALTER TABLE referral_links VALIDATE CONSTRAINT referral_links_share_code_check;
RESET statement_timeout;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
SET statement_timeout = 0;
DROP INDEX CONCURRENTLY IF EXISTS referral_links_share_code_unique;
RESET statement_timeout;
RESET lock_timeout;
