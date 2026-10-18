-- filename: 20261016170317543_outbound_ai_unmetered_video.sql
-- ADR-1432: an AI integration refuses writes the gateway cannot meter
-- unless its AI policy allows them, and video generation is priced per
-- second and rolled up in seconds. Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_allow_unmetered boolean NOT NULL DEFAULT false;
ALTER TABLE outbound_ai_price_entries
    ADD COLUMN IF NOT EXISTS millicents_per_video_second bigint;
ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS video_seconds bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_allow_unmetered_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_allow_unmetered_chk
            CHECK (NOT ai_allow_unmetered OR ai_provider IS NOT NULL);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_price_entries_video_chk' AND conrelid = 'outbound_ai_price_entries'::regclass) THEN
        ALTER TABLE outbound_ai_price_entries ADD CONSTRAINT outbound_ai_price_entries_video_chk
            CHECK (millicents_per_video_second IS NULL OR millicents_per_video_second BETWEEN 0 AND 10000000);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_video_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_video_chk CHECK (video_seconds >= 0);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily
    DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_video_chk,
    DROP COLUMN IF EXISTS video_seconds;
ALTER TABLE outbound_ai_price_entries
    DROP CONSTRAINT IF EXISTS outbound_ai_price_entries_video_chk,
    DROP COLUMN IF EXISTS millicents_per_video_second;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_allow_unmetered_chk,
    DROP COLUMN IF EXISTS ai_allow_unmetered;
-- +goose StatementEnd
