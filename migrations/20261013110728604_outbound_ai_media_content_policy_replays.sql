-- filename: 20261013110728604_outbound_ai_media_content_policy_replays.sql
-- ADR-997: AI gateway phase 7. Price lists carry media prices (image tokens,
-- per image, per audio minute, per million characters) and the UTC-day
-- rollup counts images, audio seconds and characters. Integrations carry a
-- content policy: an action per detector and blocked terms. Additive and
-- replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_ai_price_entries
    ADD COLUMN IF NOT EXISTS input_image_millicents_per_mtok bigint,
    ADD COLUMN IF NOT EXISTS output_image_millicents_per_mtok bigint,
    ADD COLUMN IF NOT EXISTS millicents_per_image bigint,
    ADD COLUMN IF NOT EXISTS millicents_per_audio_minute bigint,
    ADD COLUMN IF NOT EXISTS millicents_per_million_characters bigint;
ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS images bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS audio_seconds bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS characters bigint NOT NULL DEFAULT 0;
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_content_email text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_content_payment_card text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_content_iban text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_content_secret text NOT NULL DEFAULT 'off',
    ADD COLUMN IF NOT EXISTS ai_content_blocked_terms text[] NOT NULL DEFAULT '{}';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_price_entries_media_chk' AND conrelid = 'outbound_ai_price_entries'::regclass) THEN
        ALTER TABLE outbound_ai_price_entries ADD CONSTRAINT outbound_ai_price_entries_media_chk
            CHECK ((input_image_millicents_per_mtok IS NULL OR input_image_millicents_per_mtok BETWEEN 0 AND 1000000000)
                   AND (output_image_millicents_per_mtok IS NULL OR output_image_millicents_per_mtok BETWEEN 0 AND 1000000000)
                   AND (millicents_per_image IS NULL OR millicents_per_image BETWEEN 0 AND 10000000)
                   AND (millicents_per_audio_minute IS NULL OR millicents_per_audio_minute BETWEEN 0 AND 10000000)
                   AND (millicents_per_million_characters IS NULL OR millicents_per_million_characters BETWEEN 0 AND 1000000000));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_media_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_media_chk
            CHECK (images >= 0 AND audio_seconds >= 0 AND characters >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_content_policy_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_content_policy_chk
            CHECK (ai_content_email IN ('off', 'redact', 'block')
                   AND ai_content_payment_card IN ('off', 'redact', 'block')
                   AND ai_content_iban IN ('off', 'redact', 'block')
                   AND ai_content_secret IN ('off', 'redact', 'block')
                   AND cardinality(ai_content_blocked_terms) <= 32
                   AND (ai_provider IS NOT NULL
                        OR (ai_content_email = 'off' AND ai_content_payment_card = 'off' AND ai_content_iban = 'off'
                            AND ai_content_secret = 'off' AND cardinality(ai_content_blocked_terms) = 0)));
    END IF;
END$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_content_policy_chk,
    DROP COLUMN IF EXISTS ai_content_blocked_terms,
    DROP COLUMN IF EXISTS ai_content_secret,
    DROP COLUMN IF EXISTS ai_content_iban,
    DROP COLUMN IF EXISTS ai_content_payment_card,
    DROP COLUMN IF EXISTS ai_content_email;
ALTER TABLE outbound_ai_usage_daily
    DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_media_chk,
    DROP COLUMN IF EXISTS characters,
    DROP COLUMN IF EXISTS audio_seconds,
    DROP COLUMN IF EXISTS images;
ALTER TABLE outbound_ai_price_entries
    DROP CONSTRAINT IF EXISTS outbound_ai_price_entries_media_chk,
    DROP COLUMN IF EXISTS millicents_per_million_characters,
    DROP COLUMN IF EXISTS millicents_per_audio_minute,
    DROP COLUMN IF EXISTS millicents_per_image,
    DROP COLUMN IF EXISTS output_image_millicents_per_mtok,
    DROP COLUMN IF EXISTS input_image_millicents_per_mtok;
-- +goose StatementEnd
