-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='mcp_catalog_monitors'::regclass AND conname='mcp_catalog_oauth_profiles_chk') THEN RETURN; END IF;
CREATE FUNCTION mcp_catalog_oauth_profiles_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; config jsonb; field text; method text;
BEGIN
 FOR view IN SELECT payload UNION ALL SELECT value FROM jsonb_each(COALESCE(payload->'roles','{}'::jsonb)) LOOP
  config:=view->'config'->'observer'->'oauth';
  IF config IS NULL OR config='null'::jsonb THEN CONTINUE; END IF;
  IF COALESCE(config->>'profile','') NOT IN ('','generic','google','oidc')
   OR COALESCE(config->>'token_auth_method','') NOT IN ('','none','client_secret_basic','client_secret_post') THEN RETURN false; END IF;
  IF config->'callback_port' IS NOT NULL AND (jsonb_typeof(config->'callback_port') IS DISTINCT FROM 'number'
   OR (config->>'callback_port')::numeric NOT BETWEEN 0 AND 65535
   OR (config->>'callback_port')::numeric <> trunc((config->>'callback_port')::numeric)) THEN RETURN false; END IF;
  FOREACH field IN ARRAY ARRAY['resource','issuer','discovery_url'] LOOP
   IF COALESCE(config->>field,'')<>'' AND (config->>field !~ '^https://[^/?#@]+(/[^?#]*)?$' OR length(config->>field)>512) THEN RETURN false; END IF;
  END LOOP;
  IF (COALESCE(config->>'issuer','')='') <> (COALESCE(config->>'discovery_url','')='') THEN RETURN false; END IF;
  method:=COALESCE(NULLIF(config->>'token_auth_method',''),CASE WHEN COALESCE(config->>'client_secret_ref','')='' THEN 'none' WHEN config->>'profile'='google' THEN 'client_secret_post' ELSE 'client_secret_basic' END);
  IF (method='none') <> (COALESCE(config->>'client_secret_ref','')='') THEN RETURN false; END IF;
  IF COALESCE(config->>'guided','false')<>'true' AND (COALESCE((config->>'callback_port')::numeric,0)<>0 OR COALESCE(config->>'discovery_url','')<>'' OR COALESCE(config->>'profile','') IN ('google','oidc')) THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_oauth_profiles_chk CHECK (mcp_catalog_oauth_profiles_valid(payload));
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL (
 SELECT m.payload AS view UNION ALL SELECT value FROM jsonb_each(COALESCE(m.payload->'roles','{}'::jsonb))
 ) views WHERE COALESCE(views.view->'config'->'observer'->'oauth','{}'::jsonb) ?| ARRAY['profile','token_auth_method','callback_port','resource','issuer','discovery_url']) THEN
 RAISE EXCEPTION 'remove MCP OAuth compatibility configuration before rollback'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_oauth_profiles_chk;
DROP FUNCTION mcp_catalog_oauth_profiles_valid(jsonb);
