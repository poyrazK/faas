-- filename: 20261013110728604_outbound_ai_media_content_policy_replays.sql
-- ADR-997: AI gateway phase 7. Price lists carry media prices (image tokens,
-- per image, per audio minute, per million characters) and the UTC-day
-- rollup counts images, audio seconds and characters. Integrations carry a
-- content policy: an action per detector and blocked terms. Captured calls
-- keep their provider path, and replays resend a captured call to up to
-- five targets: apid inserts a replay, outboundd claims it and writes the
-- results. Additive and replay-safe.

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
-- Replays settle under their own caller.
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller IN ('runs', 'replay') OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');

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

ALTER TABLE outbound_ai_request_content
    ADD COLUMN IF NOT EXISTS request_path text;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_request_content_path_chk' AND conrelid = 'outbound_ai_request_content'::regclass) THEN
        ALTER TABLE outbound_ai_request_content ADD CONSTRAINT outbound_ai_request_content_path_chk
            CHECK (request_path IS NULL OR (length(request_path) BETWEEN 1 AND 2048 AND request_path ~ '^/[!-~]*$'));
    END IF;
END$$;

CREATE TABLE IF NOT EXISTS outbound_ai_replays (
    id                     uuid PRIMARY KEY,
    account_id             uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    integration_id         uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    log_id                 uuid NOT NULL REFERENCES outbound_ai_request_log (id) ON DELETE CASCADE,
    target_integration_ids uuid[] NOT NULL,
    target_models          text[] NOT NULL,
    state                  text NOT NULL DEFAULT 'pending',
    error_code             text,
    source_output_text     text,
    created_at             timestamptz NOT NULL DEFAULT now(),
    started_at             timestamptz,
    finished_at            timestamptz,
    CONSTRAINT outbound_ai_replays_state_chk CHECK (state IN ('pending', 'running', 'done', 'failed')),
    CONSTRAINT outbound_ai_replays_targets_chk
        CHECK (cardinality(target_integration_ids) BETWEEN 1 AND 5
               AND cardinality(target_models) = cardinality(target_integration_ids)),
    CONSTRAINT outbound_ai_replays_error_chk
        CHECK (error_code IS NULL OR (length(error_code) BETWEEN 1 AND 64 AND error_code ~ '^[a-z0-9_]+$')),
    CONSTRAINT outbound_ai_replays_text_chk CHECK (source_output_text IS NULL OR octet_length(source_output_text) <= 4096)
);
CREATE INDEX IF NOT EXISTS outbound_ai_replays_daily_idx ON outbound_ai_replays (integration_id, created_at);
CREATE INDEX IF NOT EXISTS outbound_ai_replays_queue_idx ON outbound_ai_replays (created_at) WHERE state IN ('pending', 'running');

CREATE TABLE IF NOT EXISTS outbound_ai_replay_results (
    replay_id             uuid NOT NULL REFERENCES outbound_ai_replays (id) ON DELETE CASCADE,
    position              smallint NOT NULL,
    target_integration_id uuid NOT NULL,
    model                 text,
    status                smallint,
    outcome               text,
    error_code            text,
    input_tokens          bigint NOT NULL DEFAULT 0,
    output_tokens         bigint NOT NULL DEFAULT 0,
    cost_millicents       bigint NOT NULL DEFAULT 0,
    duration_ms           integer NOT NULL DEFAULT 0,
    ttfb_ms               integer,
    response_body         bytea NOT NULL,
    response_truncated    boolean NOT NULL,
    response_content_type text,
    output_text           text NOT NULL DEFAULT '',
    PRIMARY KEY (replay_id, position),
    CONSTRAINT outbound_ai_replay_results_position_chk CHECK (position BETWEEN 0 AND 4),
    CONSTRAINT outbound_ai_replay_results_model_chk CHECK (model IS NULL OR (length(model) BETWEEN 1 AND 128 AND model ~ '^[ -~]+$')),
    CONSTRAINT outbound_ai_replay_results_status_chk CHECK (status IS NULL OR status BETWEEN 100 AND 599),
    CONSTRAINT outbound_ai_replay_results_outcome_chk CHECK (outcome IS NULL OR outcome IN ('known', 'unknown', 'error')),
    CONSTRAINT outbound_ai_replay_results_error_chk
        CHECK (error_code IS NULL OR (length(error_code) BETWEEN 1 AND 64 AND error_code ~ '^[a-z0-9_]+$')),
    CONSTRAINT outbound_ai_replay_results_values_chk
        CHECK (input_tokens >= 0 AND output_tokens >= 0 AND cost_millicents >= 0 AND duration_ms >= 0
               AND (ttfb_ms IS NULL OR ttfb_ms >= 0) AND octet_length(response_body) <= 32768
               AND octet_length(output_text) <= 4096),
    CONSTRAINT outbound_ai_replay_results_type_chk
        CHECK (response_content_type IS NULL
               OR (length(response_content_type) BETWEEN 1 AND 128 AND response_content_type ~ '^[ -~]+$'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_replay_results;
DROP TABLE IF EXISTS outbound_ai_replays;
ALTER TABLE outbound_ai_request_content
    DROP CONSTRAINT IF EXISTS outbound_ai_request_content_path_chk,
    DROP COLUMN IF EXISTS request_path;
DELETE FROM outbound_ai_usage_daily WHERE caller = 'replay';
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller = 'runs' OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
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
