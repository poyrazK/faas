-- filename: 20261017143415206_outbound_ai_cache_savings.sql
-- ADR-1440: AI gateway phase 17. Response-cache hits settle as cached
-- requests of the caller and model, with what the provider call would have
-- cost, and settlement records what provider prompt caching saved, which
-- is negative when cache writes outweigh reads. Additive and replay-safe.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS cached_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_saved_millicents bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS prompt_cache_saved_millicents bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_cache_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_cache_chk
            CHECK (cached_requests BETWEEN 0 AND requests AND cache_saved_millicents >= 0);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily
    DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_cache_chk,
    DROP COLUMN IF EXISTS prompt_cache_saved_millicents,
    DROP COLUMN IF EXISTS cache_saved_millicents,
    DROP COLUMN IF EXISTS cached_requests;
-- +goose StatementEnd
