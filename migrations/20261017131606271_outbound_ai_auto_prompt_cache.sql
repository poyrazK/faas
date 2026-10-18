-- filename: 20261017131606271_outbound_ai_auto_prompt_cache.sql
-- ADR-1420: AI gateway phase 15. An AI integration can let the gateway add
-- prompt-cache breakpoints to Claude requests that carry none. Like the
-- other AI policy switches it needs an AI provider. Additive and
-- replay-safe.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_integrations ADD COLUMN IF NOT EXISTS ai_auto_prompt_cache boolean NOT NULL DEFAULT false;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_auto_prompt_cache_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_auto_prompt_cache_chk
            CHECK (NOT ai_auto_prompt_cache OR ai_provider IS NOT NULL);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_auto_prompt_cache_chk,
    DROP COLUMN IF EXISTS ai_auto_prompt_cache;
-- +goose StatementEnd
