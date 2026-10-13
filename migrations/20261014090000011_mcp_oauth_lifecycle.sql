-- +goose Up
-- The final schema object marks this transactional Up as applied. A ledger
-- replay must preserve definitions widened by later migrations and must not
-- re-run grant backfills or replace saved rollback constraint definitions.
-- +goose StatementBegin
DO $mcp_replay_guard$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='mcp_catalog_monitors'::regclass AND conname='mcp_catalog_oauth_lifecycle_chk') THEN RETURN; END IF;
CREATE FUNCTION mcp_catalog_oauth_lifecycle_valid(payload jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE view jsonb; config jsonb; lifecycle jsonb; job jsonb; event jsonb;
BEGIN
 FOR view IN SELECT payload UNION ALL SELECT value FROM jsonb_each(COALESCE(payload->'roles','{}'::jsonb)) LOOP
  config:=view->'config'->'observer'->'oauth';
  IF COALESCE(config->>'revocation_endpoint','')<>'' AND (config->>'revocation_endpoint' !~ '^https://[^/?#@:\\[:space:]]+(/[^?#\\[:space:]]*)?$' OR length(config->>'revocation_endpoint')>512) THEN RETURN false; END IF;
  IF COALESCE(config->>'revocation_auth_method','') NOT IN ('','none','client_secret_basic','client_secret_post') THEN RETURN false; END IF;
  IF COALESCE(config->>'revocation_auth_method','') IN ('client_secret_basic','client_secret_post') AND COALESCE(config->>'client_secret_ref','')='' THEN RETURN false; END IF;
  IF COALESCE(config->>'revocation_auth_method','')<>'' AND COALESCE(config->>'revocation_endpoint','')='' THEN RETURN false; END IF;
  lifecycle:=view->'oauth_lifecycle'; IF lifecycle IS NULL OR lifecycle='null'::jsonb THEN CONTINUE; END IF;
  IF lifecycle->>'local_state' IS NULL OR lifecycle->>'local_state' NOT IN ('connected','disconnected','reauthorization_required') OR jsonb_typeof(lifecycle->'history') IS DISTINCT FROM 'array' THEN RETURN false; END IF;
  IF jsonb_array_length(lifecycle->'history')>64 THEN RETURN false; END IF;
  FOR event IN SELECT value FROM jsonb_array_elements(lifecycle->'history') LOOP
   IF event->>'kind' IS NULL OR event->>'kind' NOT IN ('connected','renewed','reauthorization_required','disconnected','revocation_acknowledged','revocation_manual_required','credentials_discarded') THEN RETURN false; END IF;
  END LOOP;
  job:=lifecycle->'revocation'; IF job IS NULL OR job='null'::jsonb THEN CONTINUE; END IF;
  IF job->>'state' IS NULL OR job->>'state' NOT IN ('pending','provider_acknowledged','manual_required') OR jsonb_typeof(job->'attempts') IS DISTINCT FROM 'number' THEN RETURN false; END IF;
  IF (job->>'attempts')::numeric NOT BETWEEN 0 AND 8 OR (job->>'attempts')::numeric<>trunc((job->>'attempts')::numeric) THEN RETURN false; END IF;
  IF COALESCE(job->>'failure_reason','') NOT IN ('','credential_unavailable','provider_unavailable') THEN RETURN false; END IF;
  IF job->>'state'='pending' AND (jsonb_typeof(job->'private_config') IS DISTINCT FROM 'object' OR COALESCE(job->>'private_ciphertext','')='' OR COALESCE(job->>'next_attempt_at','')='') THEN RETURN false; END IF;
  IF job->>'state'<>'pending' AND (job ? 'private_client_ciphertext' OR job ? 'private_config' OR job ? 'private_ciphertext' OR job ? 'private_lease_id' OR job ? 'private_lease_until') THEN RETURN false; END IF;
 END LOOP;
 RETURN true;
END $$;

ALTER TABLE mcp_catalog_monitors ADD CONSTRAINT mcp_catalog_oauth_lifecycle_chk CHECK (mcp_catalog_oauth_lifecycle_valid(payload));
END $mcp_replay_guard$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM mcp_catalog_monitors m CROSS JOIN LATERAL (SELECT m.payload AS view UNION ALL SELECT value FROM jsonb_each(COALESCE(m.payload->'roles','{}'::jsonb))) views WHERE views.view ? 'oauth_lifecycle' OR COALESCE(views.view->'config'->'observer'->'oauth','{}'::jsonb) ?| ARRAY['revocation_endpoint','revocation_auth_method']) THEN
 RAISE EXCEPTION 'retire and remove MCP OAuth lifecycle data before rollback'; END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mcp_catalog_monitors DROP CONSTRAINT mcp_catalog_oauth_lifecycle_chk;
DROP FUNCTION mcp_catalog_oauth_lifecycle_valid(jsonb);
