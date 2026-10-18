-- filename: 20261016181201526_outbound_ai_tool_fees.sql
-- ADR-1380: price lists price built-in tools per use (web search, file
-- search, and code-interpreter container sessions per GB), and the daily
-- rollup counts them. Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_price_entries
    ADD COLUMN IF NOT EXISTS millicents_per_web_search bigint,
    ADD COLUMN IF NOT EXISTS millicents_per_file_search bigint,
    ADD COLUMN IF NOT EXISTS millicents_per_code_session_gb bigint;
ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS web_searches bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS file_searches bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS code_session_gb bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_price_entries_tools_chk' AND conrelid = 'outbound_ai_price_entries'::regclass) THEN
        ALTER TABLE outbound_ai_price_entries ADD CONSTRAINT outbound_ai_price_entries_tools_chk
            CHECK ((millicents_per_web_search IS NULL OR millicents_per_web_search BETWEEN 0 AND 10000000)
                   AND (millicents_per_file_search IS NULL OR millicents_per_file_search BETWEEN 0 AND 10000000)
                   AND (millicents_per_code_session_gb IS NULL OR millicents_per_code_session_gb BETWEEN 0 AND 10000000));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_tools_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_tools_chk
            CHECK (web_searches >= 0 AND file_searches >= 0 AND code_session_gb >= 0);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_usage_daily
    DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_tools_chk,
    DROP COLUMN IF EXISTS code_session_gb,
    DROP COLUMN IF EXISTS file_searches,
    DROP COLUMN IF EXISTS web_searches;
ALTER TABLE outbound_ai_price_entries
    DROP CONSTRAINT IF EXISTS outbound_ai_price_entries_tools_chk,
    DROP COLUMN IF EXISTS millicents_per_code_session_gb,
    DROP COLUMN IF EXISTS millicents_per_file_search,
    DROP COLUMN IF EXISTS millicents_per_web_search;
-- +goose StatementEnd
