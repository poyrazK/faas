-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='mcp_catalog_monitors'::regclass AND conname='mcp_catalog_oauth_flows_chk') THEN RETURN; END IF;
CREATE OR REPLACE FUNCTION mcp_catalog_observer_oauth_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; config jsonb; runtime jsonb;
BEGIN
 FOR view IN SELECT payload UNION ALL SELECT value FROM jsonb_each(COALESCE(payload->'roles','{}'::jsonb)) LOOP
  config := view->'config'->'observer'->'oauth'; runtime := view->'observer_status'->'oauth';
  IF config IS NOT NULL AND config <> 'null'::jsonb THEN
   IF jsonb_typeof(config) IS DISTINCT FROM 'object'
    OR COALESCE(config->>'token_endpoint','') !~ '^https://[^/?#@]+(/[^?#]*)?$'
    OR length(config->>'token_endpoint') > 512
    OR COALESCE(length(config->>'client_id'),0) NOT BETWEEN 1 AND 512
    OR (COALESCE(config->>'guided','false') <> 'true' AND COALESCE(config->>'refresh_token_secret_ref','') !~ '^secret:[A-Za-z_][A-Za-z0-9_]*$')
    OR (config->>'guided'='true' AND (COALESCE(config->>'refresh_token_secret_ref','') <> '' OR COALESCE(config->>'authorization_endpoint','') !~ '^https://[^/?#@]+(/[^?#]*)?$'))
    OR (COALESCE(config->>'client_secret_ref','') <> '' AND config->>'client_secret_ref' !~ '^secret:[A-Za-z_][A-Za-z0-9_]*$')
    OR COALESCE(view->'config'->'observer'->>'secret_ref','') <> '' THEN RETURN false; END IF;
  END IF;
  IF runtime IS NOT NULL AND runtime <> 'null'::jsonb THEN
   IF config IS NULL OR config='null'::jsonb
    OR jsonb_typeof(runtime) IS DISTINCT FROM 'object'
    OR COALESCE(runtime->>'state','') NOT IN ('connected','reauthorization_required')
    OR COALESCE(runtime->>'source_sha256','') !~ '^[a-f0-9]{64}$'
    OR COALESCE(length(runtime->>'ciphertext'),0) NOT BETWEEN 1 AND 87384
    OR (runtime->>'state'='connected' AND (runtime->>'expires_at' IS NULL OR runtime->>'last_renewal_at' IS NULL)) THEN RETURN false; END IF;
  END IF;
 END LOOP;
 RETURN true;
END $$;



CREATE FUNCTION mcp_catalog_oauth_flows_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; flow jsonb;
BEGIN
 FOR view IN SELECT payload UNION ALL SELECT value FROM jsonb_each(COALESCE(payload->'roles','{}'::jsonb)) LOOP
  flow:=view->'observer_status'->'oauth_flow';
  IF flow IS NOT NULL AND flow<>'null'::jsonb THEN
   IF view->'config'->'observer'->'oauth'->>'guided' IS DISTINCT FROM 'true'
    OR jsonb_typeof(flow) IS DISTINCT FROM 'object'
    OR COALESCE(flow->>'state_sha256','') !~ '^[a-f0-9]{64}$'
    OR COALESCE(length(flow->>'ciphertext'),0) NOT BETWEEN 1 AND 87384
    OR jsonb_typeof(flow->'consumed') IS DISTINCT FROM 'boolean'
    OR jsonb_typeof(flow->'expires_at') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
  END IF;
 END LOOP;
 RETURN true;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_oauth_flows_chk CHECK (mcp_catalog_oauth_flows_valid(payload));
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL (
 SELECT m.payload AS view UNION ALL SELECT value FROM jsonb_each(COALESCE(m.payload->'roles','{}'::jsonb))
 ) views WHERE views.view->'config'->'observer'->'oauth'->>'guided'='true') THEN
 RAISE EXCEPTION 'remove guided MCP OAuth configuration before rollback'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_oauth_flows_chk;
DROP FUNCTION mcp_catalog_oauth_flows_valid(jsonb);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION mcp_catalog_observer_oauth_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; config jsonb; runtime jsonb;
BEGIN
 FOR view IN SELECT payload UNION ALL SELECT value FROM jsonb_each(COALESCE(payload->'roles','{}'::jsonb)) LOOP
  config := view->'config'->'observer'->'oauth'; runtime := view->'observer_status'->'oauth';
  IF config IS NOT NULL AND config <> 'null'::jsonb THEN
   IF jsonb_typeof(config) IS DISTINCT FROM 'object'
    OR COALESCE(config->>'token_endpoint','') !~ '^https://[^/?#@]+(/[^?#]*)?$'
    OR length(config->>'token_endpoint') > 512
    OR COALESCE(length(config->>'client_id'),0) NOT BETWEEN 1 AND 512
    OR COALESCE(config->>'refresh_token_secret_ref','') !~ '^secret:[A-Za-z_][A-Za-z0-9_]*$'
    OR (COALESCE(config->>'client_secret_ref','') <> '' AND config->>'client_secret_ref' !~ '^secret:[A-Za-z_][A-Za-z0-9_]*$')
    OR COALESCE(view->'config'->'observer'->>'secret_ref','') <> '' THEN RETURN false; END IF;
  END IF;
  IF runtime IS NOT NULL AND runtime <> 'null'::jsonb THEN
   IF config IS NULL OR config='null'::jsonb
    OR jsonb_typeof(runtime) IS DISTINCT FROM 'object'
    OR COALESCE(runtime->>'state','') NOT IN ('connected','reauthorization_required')
    OR COALESCE(runtime->>'source_sha256','') !~ '^[a-f0-9]{64}$'
    OR COALESCE(length(runtime->>'ciphertext'),0) NOT BETWEEN 1 AND 87384
    OR (runtime->>'state'='connected' AND (runtime->>'expires_at' IS NULL OR runtime->>'last_renewal_at' IS NULL)) THEN RETURN false; END IF;
  END IF;
 END LOOP;
 RETURN true;
END $$;

-- +goose StatementEnd
