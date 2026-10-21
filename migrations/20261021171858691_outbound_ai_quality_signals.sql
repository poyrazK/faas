-- filename: 20261021171858691_outbound_ai_quality_signals.sql
-- ADR 20261021171712: quality signals. The request log keeps each call's
-- quality flags, its feedback and its online evaluation, and the daily
-- usage rollup counts them. The checks are added NOT VALID;
-- 20261021171859791 validates them and indexes pending evaluations.
-- 20261021171858793 adds the integration settings and alert tables, and
-- 20261021171858895 the webhook event. Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_request_log
    ADD COLUMN IF NOT EXISTS quality_flags text[],
    ADD COLUMN IF NOT EXISTS feedback text,
    ADD COLUMN IF NOT EXISTS feedback_label text,
    ADD COLUMN IF NOT EXISTS feedback_at timestamptz,
    ADD COLUMN IF NOT EXISTS evaluation_state text,
    ADD COLUMN IF NOT EXISTS evaluation_score smallint;

ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS truncated_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refused_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS empty_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS invalid_json_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tool_error_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS good_feedback bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS bad_feedback bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS evaluated_requests bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS evaluation_score_sum bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS evaluation_errors bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_request_log_quality_chk' AND conrelid = 'outbound_ai_request_log'::regclass) THEN
        ALTER TABLE outbound_ai_request_log ADD CONSTRAINT outbound_ai_request_log_quality_chk
            CHECK ((quality_flags IS NULL
                    OR (cardinality(quality_flags) <= 5
                        AND quality_flags <@ ARRAY['truncated', 'refused', 'empty', 'invalid_json', 'tool_error']::text[]))
                   AND (feedback IS NULL OR feedback IN ('good', 'bad'))
                   AND (feedback_label IS NULL OR (feedback IS NOT NULL AND feedback_label ~ '^[a-z][a-z0-9_.-]{0,31}$'))
                   AND ((feedback IS NULL) = (feedback_at IS NULL))
                   AND (evaluation_state IS NULL OR evaluation_state IN ('pending', 'done', 'error'))
                   AND (evaluation_score IS NULL OR (evaluation_state = 'done' AND evaluation_score BETWEEN 1 AND 5))) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_quality_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_quality_chk
            CHECK (truncated_requests >= 0 AND refused_requests >= 0 AND empty_requests >= 0 AND invalid_json_requests >= 0
                   AND tool_error_requests >= 0 AND good_feedback >= 0 AND bad_feedback >= 0 AND evaluated_requests >= 0
                   AND evaluation_score_sum BETWEEN evaluated_requests AND 5 * evaluated_requests
                   AND evaluation_errors >= 0) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_quality_chk,
    DROP COLUMN IF EXISTS evaluation_errors, DROP COLUMN IF EXISTS evaluation_score_sum, DROP COLUMN IF EXISTS evaluated_requests,
    DROP COLUMN IF EXISTS bad_feedback, DROP COLUMN IF EXISTS good_feedback, DROP COLUMN IF EXISTS tool_error_requests,
    DROP COLUMN IF EXISTS invalid_json_requests, DROP COLUMN IF EXISTS empty_requests, DROP COLUMN IF EXISTS refused_requests,
    DROP COLUMN IF EXISTS truncated_requests;
ALTER TABLE outbound_ai_request_log DROP CONSTRAINT IF EXISTS outbound_ai_request_log_quality_chk,
    DROP COLUMN IF EXISTS evaluation_score, DROP COLUMN IF EXISTS evaluation_state, DROP COLUMN IF EXISTS feedback_at,
    DROP COLUMN IF EXISTS feedback_label, DROP COLUMN IF EXISTS feedback, DROP COLUMN IF EXISTS quality_flags;
-- +goose StatementEnd
