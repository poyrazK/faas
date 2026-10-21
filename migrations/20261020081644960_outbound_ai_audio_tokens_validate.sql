-- filename: 20261020081644960_outbound_ai_audio_tokens_validate.sql
-- ADR 20261019072722: validate the audio checks that
-- 20261019073407590_outbound_ai_audio_tokens added NOT VALID. In its own
-- transaction VALIDATE takes a SHARE UPDATE EXCLUSIVE lock, so price lists
-- and the daily usage rollup keep taking writes during the scan.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE outbound_ai_price_entries VALIDATE CONSTRAINT outbound_ai_price_entries_audio_chk;
ALTER TABLE outbound_ai_usage_daily VALIDATE CONSTRAINT outbound_ai_usage_daily_audio_chk;

-- +goose Down
SELECT 1;
