-- filename: 20261011111507223_outbound_ai_gateway.sql
-- ADR-979: AI gateway mode on outbound integrations. Additive and replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_provider text,
    ADD COLUMN IF NOT EXISTS ai_daily_token_limit bigint;

ALTER TABLE outbound_admission_state
    ADD COLUMN IF NOT EXISTS daily_ai_token_count bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_ai_provider_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_ai_provider_chk
                CHECK (ai_provider IS NULL OR ai_provider IN ('openai', 'anthropic'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_integrations_ai_daily_token_limit_chk'
          AND conrelid = 'outbound_integrations'::regclass
    ) THEN
        ALTER TABLE outbound_integrations
            ADD CONSTRAINT outbound_integrations_ai_daily_token_limit_chk
                CHECK (ai_daily_token_limit IS NULL
                       OR (ai_provider IS NOT NULL AND ai_daily_token_limit BETWEEN 1 AND 100000000000));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conname = 'outbound_admission_state_daily_ai_token_count_chk'
          AND conrelid = 'outbound_admission_state'::regclass
    ) THEN
        ALTER TABLE outbound_admission_state
            ADD CONSTRAINT outbound_admission_state_daily_ai_token_count_chk
                CHECK (daily_ai_token_count >= 0);
    END IF;
END$$;

-- UTC-day AI usage per integration, caller and model. caller is an app UUID
-- or 'runs'. outboundd is the only writer and prunes rows after 90 days.
CREATE TABLE IF NOT EXISTS outbound_ai_usage_daily (
    integration_id         uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    usage_date             date NOT NULL,
    caller                 text NOT NULL,
    model                  text NOT NULL,
    requests               bigint NOT NULL DEFAULT 0,
    error_requests         bigint NOT NULL DEFAULT 0,
    usage_unknown_requests bigint NOT NULL DEFAULT 0,
    input_tokens           bigint NOT NULL DEFAULT 0,
    cached_input_tokens    bigint NOT NULL DEFAULT 0,
    cache_write_tokens     bigint NOT NULL DEFAULT 0,
    output_tokens          bigint NOT NULL DEFAULT 0,
    updated_at             timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, usage_date, caller, model),
    CONSTRAINT outbound_ai_usage_daily_caller_chk
        CHECK (caller = 'runs' OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    CONSTRAINT outbound_ai_usage_daily_model_chk
        CHECK (length(model) BETWEEN 1 AND 128 AND model ~ '^[ -~]+$'),
    CONSTRAINT outbound_ai_usage_daily_counts_chk
        CHECK (requests >= 0 AND error_requests >= 0 AND usage_unknown_requests >= 0
               AND error_requests + usage_unknown_requests <= requests
               AND input_tokens >= 0 AND cached_input_tokens >= 0
               AND cache_write_tokens >= 0 AND output_tokens >= 0)
);

CREATE INDEX IF NOT EXISTS outbound_ai_usage_daily_usage_date_idx
    ON outbound_ai_usage_daily (usage_date);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_usage_daily;
ALTER TABLE outbound_admission_state
    DROP CONSTRAINT IF EXISTS outbound_admission_state_daily_ai_token_count_chk,
    DROP COLUMN IF EXISTS daily_ai_token_count;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_daily_token_limit_chk,
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_provider_chk,
    DROP COLUMN IF EXISTS ai_daily_token_limit,
    DROP COLUMN IF EXISTS ai_provider;
-- +goose StatementEnd
