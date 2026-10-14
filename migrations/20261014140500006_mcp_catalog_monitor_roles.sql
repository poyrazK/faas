-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='mcp_catalog_monitors'::regclass AND conname='mcp_catalog_monitor_roles_chk') THEN RETURN; END IF;
CREATE FUNCTION mcp_catalog_monitor_roles_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE role_name text; view jsonb; role_count integer := 0;
BEGIN
 IF payload ? 'roles' THEN
  IF jsonb_typeof(payload->'roles') <> 'object' THEN RETURN false; END IF;
  FOR role_name, view IN SELECT * FROM jsonb_each(payload->'roles') LOOP
   role_count := role_count + 1;
   IF role_name = 'default' OR role_name !~ '^[a-z][a-z0-9_-]{0,31}$'
    OR view ? 'roles'
    OR jsonb_typeof(view->'config') IS DISTINCT FROM 'object'
    OR view->'config'->>'role' IS DISTINCT FROM role_name
    OR COALESCE(view->'config'->>'revision','') = ''
    OR jsonb_typeof(view->'history') IS DISTINCT FROM 'array' THEN RETURN false; END IF;
   IF jsonb_array_length(view->'history') > 32 THEN RETURN false; END IF;
   IF view->'latest' IS NOT NULL AND view->'latest' <> 'null'::jsonb
    AND COALESCE(view->'latest'->>'state','') NOT IN ('healthy','drift','unavailable') THEN RETURN false; END IF;
  END LOOP;
 END IF;
 IF COALESCE(payload->'config'->>'revision','') <> '' THEN role_count := role_count + 1; END IF;
 RETURN role_count <= 16;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_monitor_roles_chk CHECK (mcp_catalog_monitor_roles_valid(payload));
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM mcp_catalog_monitors WHERE payload ? 'roles' AND payload->'roles' <> '{}'::jsonb) THEN
  RAISE EXCEPTION 'remove named MCP catalog monitor roles before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_monitor_roles_chk;
DROP FUNCTION mcp_catalog_monitor_roles_valid(jsonb);
