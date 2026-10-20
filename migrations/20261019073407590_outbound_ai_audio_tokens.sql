-- filename: 20261019073407590_outbound_ai_audio_tokens.sql
-- ADR 20261019072722: AI gateway phase 18. Price lists price audio tokens
-- apart from text tokens (input, cached input and output), and the daily
-- rollup counts the audio part of input and output tokens. Additive and
-- replay-safe. The checks are added NOT VALID so existing rows are not
-- scanned under the write-blocking lock; 20261020081644960 validates them.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_price_entries
    ADD COLUMN IF NOT EXISTS input_audio_millicents_per_mtok bigint,
    ADD COLUMN IF NOT EXISTS cached_input_audio_millicents_per_mtok bigint,
    ADD COLUMN IF NOT EXISTS output_audio_millicents_per_mtok bigint;
ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS input_audio_tokens bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_audio_tokens bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_price_entries_audio_chk' AND conrelid = 'outbound_ai_price_entries'::regclass) THEN
        ALTER TABLE outbound_ai_price_entries ADD CONSTRAINT outbound_ai_price_entries_audio_chk
            CHECK ((input_audio_millicents_per_mtok IS NULL OR input_audio_millicents_per_mtok BETWEEN 0 AND 1000000000)
                   AND (cached_input_audio_millicents_per_mtok IS NULL OR cached_input_audio_millicents_per_mtok BETWEEN 0 AND 1000000000)
                   AND (output_audio_millicents_per_mtok IS NULL OR output_audio_millicents_per_mtok BETWEEN 0 AND 1000000000)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_audio_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_audio_chk
            CHECK (input_audio_tokens BETWEEN 0 AND input_tokens AND output_audio_tokens BETWEEN 0 AND output_tokens) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily
    DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_audio_chk,
    DROP COLUMN IF EXISTS output_audio_tokens,
    DROP COLUMN IF EXISTS input_audio_tokens;
ALTER TABLE outbound_ai_price_entries
    DROP CONSTRAINT IF EXISTS outbound_ai_price_entries_audio_chk,
    DROP COLUMN IF EXISTS output_audio_millicents_per_mtok,
    DROP COLUMN IF EXISTS cached_input_audio_millicents_per_mtok,
    DROP COLUMN IF EXISTS input_audio_millicents_per_mtok;
-- +goose StatementEnd
