-- filename: 20261012080904121_outbound_ai_end_users.sql
-- ADR-994: per-end-user AI attribution and daily limits. An app names the
-- end user of a call with X-Gregale-AI-User; outboundd rolls usage up per
-- integration, UTC day and end user, and a lease carries its end user so
-- live reservations count against that user's hard caps. Additive and
-- replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_user_daily_token_limit bigint,
    ADD COLUMN IF NOT EXISTS ai_user_daily_cost_limit_millicents bigint;
ALTER TABLE outbound_admission_leases
    ADD COLUMN IF NOT EXISTS ai_end_user text;
ALTER TABLE outbound_ai_request_log
    ADD COLUMN IF NOT EXISTS end_user text;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_user_limits_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_user_limits_chk
            CHECK ((ai_user_daily_token_limit IS NULL
                    OR (ai_provider IS NOT NULL AND ai_user_daily_token_limit BETWEEN 1 AND 100000000000))
                   AND (ai_user_daily_cost_limit_millicents IS NULL
                    OR (ai_provider IS NOT NULL AND ai_user_daily_cost_limit_millicents BETWEEN 1 AND 1000000000000)));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_admission_leases_ai_end_user_chk' AND conrelid = 'outbound_admission_leases'::regclass) THEN
        ALTER TABLE outbound_admission_leases ADD CONSTRAINT outbound_admission_leases_ai_end_user_chk
            CHECK (ai_end_user IS NULL OR ai_end_user ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_request_log_end_user_chk' AND conrelid = 'outbound_ai_request_log'::regclass) THEN
        ALTER TABLE outbound_ai_request_log ADD CONSTRAINT outbound_ai_request_log_end_user_chk
            CHECK (end_user IS NULL OR end_user ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$');
    END IF;
END$$;

-- One row per integration, UTC day and end user. Beyond the per-day cap of
-- distinct end users, calls aggregate under '~other', which no header value
-- can name.
CREATE TABLE IF NOT EXISTS outbound_ai_user_usage_daily (
    integration_id  uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    usage_date      date NOT NULL,
    end_user        text NOT NULL,
    requests        bigint NOT NULL DEFAULT 0,
    error_requests  bigint NOT NULL DEFAULT 0,
    input_tokens    bigint NOT NULL DEFAULT 0,
    output_tokens   bigint NOT NULL DEFAULT 0,
    cost_millicents bigint NOT NULL DEFAULT 0,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, usage_date, end_user),
    CONSTRAINT outbound_ai_user_usage_daily_end_user_chk
        CHECK (end_user ~ '^[A-Za-z0-9._:@+=/|-]{1,64}$' OR end_user = '~other'),
    CONSTRAINT outbound_ai_user_usage_daily_values_chk
        CHECK (requests >= 0 AND error_requests BETWEEN 0 AND requests AND input_tokens >= 0
               AND output_tokens >= 0 AND cost_millicents >= 0)
);
CREATE INDEX IF NOT EXISTS outbound_ai_user_usage_daily_prune_idx
    ON outbound_ai_user_usage_daily (usage_date);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_user_usage_daily;
ALTER TABLE outbound_ai_request_log
    DROP CONSTRAINT IF EXISTS outbound_ai_request_log_end_user_chk,
    DROP COLUMN IF EXISTS end_user;
ALTER TABLE outbound_admission_leases
    DROP CONSTRAINT IF EXISTS outbound_admission_leases_ai_end_user_chk,
    DROP COLUMN IF EXISTS ai_end_user;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_user_limits_chk,
    DROP COLUMN IF EXISTS ai_user_daily_cost_limit_millicents,
    DROP COLUMN IF EXISTS ai_user_daily_token_limit;
-- +goose StatementEnd
