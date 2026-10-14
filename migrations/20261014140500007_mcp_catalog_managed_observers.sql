-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='mcp_catalog_monitors'::regclass AND conname='mcp_catalog_managed_observers_chk') THEN RETURN; END IF;
CREATE FUNCTION mcp_catalog_managed_observers_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; config jsonb; runtime jsonb;
BEGIN
 FOR view IN SELECT payload UNION ALL SELECT value FROM jsonb_each(COALESCE(payload->'roles','{}'::jsonb)) LOOP
  config := view->'config'->'observer'; runtime := view->'observer_status';
  IF config IS NOT NULL AND config <> 'null'::jsonb THEN
   IF jsonb_typeof(config) IS DISTINCT FROM 'object'
    OR jsonb_typeof(config->'enabled') IS DISTINCT FROM 'boolean'
    OR jsonb_typeof(config->'paused') IS DISTINCT FROM 'boolean'
    OR jsonb_typeof(config->'interval_seconds') IS DISTINCT FROM 'number'
    OR jsonb_typeof(config->'timeout_seconds') IS DISTINCT FROM 'number' THEN RETURN false; END IF;
   IF (config->>'interval_seconds')::numeric NOT BETWEEN 30 AND 3600
    OR (config->>'timeout_seconds')::numeric NOT BETWEEN 1 AND 30
    OR COALESCE(config->>'endpoint','') !~ '^/([^/].*)?$'
    OR length(config->>'endpoint') > 512
    OR (COALESCE(config->>'secret_ref','') <> '' AND config->>'secret_ref' !~ '^secret:[A-Za-z_][A-Za-z0-9_]*$') THEN RETURN false; END IF;
  END IF;
  IF runtime IS NOT NULL AND runtime <> 'null'::jsonb THEN
   IF config IS NULL OR config='null'::jsonb OR jsonb_typeof(runtime) IS DISTINCT FROM 'object'
    OR COALESCE(runtime->>'state','') NOT IN ('scheduled','checking','degraded')
    OR jsonb_typeof(runtime->'consecutive_failures') IS DISTINCT FROM 'number' THEN RETURN false; END IF;
   IF (runtime->>'consecutive_failures')::numeric NOT BETWEEN 0 AND 16
    OR COALESCE(runtime->>'failure_reason','') NOT IN ('','credential_unavailable','source_unavailable','comparison_unavailable') THEN RETURN false; END IF;
  END IF;
 END LOOP;
 RETURN true;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_managed_observers_chk CHECK (mcp_catalog_managed_observers_valid(payload));
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL (
  SELECT m.payload AS view UNION ALL SELECT value FROM jsonb_each(COALESCE(m.payload->'roles','{}'::jsonb))
 ) views WHERE views.view->'config'->'observer' IS NOT NULL AND views.view->'config'->'observer'<>'null'::jsonb) THEN
  RAISE EXCEPTION 'remove managed MCP observer configuration before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_managed_observers_chk;
DROP FUNCTION mcp_catalog_managed_observers_valid(jsonb);
