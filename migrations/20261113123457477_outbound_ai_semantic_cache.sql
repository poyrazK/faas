-- filename: 20261113123457477_outbound_ai_semantic_cache.sql
-- ADR 20261112194947: an AI integration can answer calls whose last user
-- turn means the same as a cached call's. Its settings are one nullable
-- jsonb object that needs an AI provider. The embedding calls settle under
-- the caller 'semantic_cache', so the usage caller CHECK accepts it.
--
-- The new CHECKs are added NOT VALID and validated in their own steps; the
-- caller CHECK is replaced online through a widened copy, as in
-- 20261028140007417_route_alert_metrics. Every step is guarded, so a rerun
-- after a partial failure converges.
-- migration-compat: expand
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE outbound_integrations ADD COLUMN IF NOT EXISTS ai_semantic_cache jsonb;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_integrations'::regclass
                AND conname = 'outbound_integrations_ai_semantic_cache_chk') THEN
  ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_semantic_cache_chk
    CHECK (ai_semantic_cache IS NULL OR (ai_provider IS NOT NULL AND jsonb_typeof(ai_semantic_cache) = 'object'
           AND octet_length(ai_semantic_cache::text) <= 1024)) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE outbound_integrations VALIDATE CONSTRAINT outbound_integrations_ai_semantic_cache_chk;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_ai_usage_daily'::regclass
                AND conname = 'outbound_ai_usage_daily_caller_semantic_chk') THEN
  ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_semantic_chk
    CHECK (caller IN ('runs', 'replay', 'moderation', 'evaluation', 'semantic_cache')
           OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE outbound_ai_usage_daily VALIDATE CONSTRAINT outbound_ai_usage_daily_caller_semantic_chk;
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'outbound_ai_usage_daily'::regclass
                AND conname = 'outbound_ai_usage_daily_caller_chk') THEN
  ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
    CHECK (caller IN ('runs', 'replay', 'moderation', 'evaluation', 'semantic_cache')
           OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$') NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE outbound_ai_usage_daily VALIDATE CONSTRAINT outbound_ai_usage_daily_caller_chk;
ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_semantic_chk;
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM outbound_ai_usage_daily WHERE caller = 'semantic_cache') THEN
  RAISE EXCEPTION 'delete semantic_cache usage rows before rollback';
 END IF;
 ALTER TABLE outbound_ai_usage_daily DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_caller_chk;
 ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_caller_chk
   CHECK (caller IN ('runs', 'replay', 'moderation', 'evaluation')
          OR caller ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
 ALTER TABLE outbound_integrations DROP CONSTRAINT IF EXISTS outbound_integrations_ai_semantic_cache_chk;
 ALTER TABLE outbound_integrations DROP COLUMN IF EXISTS ai_semantic_cache;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
