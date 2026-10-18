-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_trigger WHERE tgname='deployment_mcp_readiness_gate' AND tgrelid='deployments'::regclass) THEN RETURN; END IF;
 CREATE TABLE app_mcp_readiness_policies(app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,policy jsonb NOT NULL);
 CREATE TABLE app_mcp_readiness_policy_history(revision uuid PRIMARY KEY,app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,policy jsonb NOT NULL);
 CREATE TABLE deployment_mcp_readiness_evidence(deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,role text NOT NULL,policy_revision uuid NOT NULL,artifact_sha256 text NOT NULL,passed boolean NOT NULL,observed_at timestamptz NOT NULL,evidence jsonb NOT NULL,PRIMARY KEY(deployment_id,role));
 CREATE TABLE deployment_mcp_readiness_overrides(id uuid PRIMARY KEY,deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,policy_revision uuid NOT NULL,artifact_sha256 text NOT NULL,reason text NOT NULL CHECK(length(reason) BETWEEN 1 AND 512),created_by uuid NOT NULL REFERENCES accounts(id),created_at timestamptz NOT NULL DEFAULT clock_timestamp(),expires_at timestamptz NOT NULL,used_at timestamptz);
 CREATE INDEX deployment_mcp_readiness_overrides_target ON deployment_mcp_readiness_overrides(deployment_id,created_at DESC);
CREATE OR REPLACE FUNCTION mcp_readiness_subject(d deployments) RETURNS text LANGUAGE sql STABLE AS $$
 SELECT encode(sha256(convert_to(jsonb_build_array(
 d.id,d.app_id,d.image_digest,d.build_id,d.rootfs_path,d.rootfs_key,d.source_sha256,d.commit_sha,d.scope,
 d.override_entrypoint,d.override_cmd,d.override_env,d.override_env_secrets,d.override_port,d.override_healthcheck,d.override_liveness_probe,d.sidecars,
 (SELECT jsonb_object_agg(k,v) FROM apps a,LATERAL jsonb_each(to_jsonb(a)) f(k,v) WHERE a.id=d.app_id AND k IN ('manifest','slug','start_command','app_protocol','visibility','consumer_auth_mode','only_declared_routes','declared_routes','public_auth_basic','public_auth_ip_allowlist','runtime','env','entrypoint','cmd','port','healthcheck','require_authn','public_auth','public_auth_mode','streaming_enabled','maintenance_mode')),
 (SELECT jsonb_agg(jsonb_build_array(key,scope,delivery_version,value_hash) ORDER BY scope,key) FROM app_secrets WHERE app_id=d.app_id AND scope=d.scope)
 )::text,'UTF8')),'hex')
$$;
CREATE OR REPLACE FUNCTION guard_deployment_mcp_readiness() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE cfg jsonb; subject text; probe_role jsonb; candidate_override uuid;
BEGIN
 IF NEW.status<>'live' OR NEW.traffic_percent<=0 THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND OLD.status='live' AND NEW.traffic_percent<=OLD.traffic_percent THEN RETURN NEW; END IF;
 SELECT policy INTO cfg FROM app_mcp_readiness_policies WHERE app_id=NEW.app_id FOR SHARE;
 IF cfg IS NULL OR NOT (cfg->>'enabled')::boolean THEN RETURN NEW; END IF;
 subject:=mcp_readiness_subject(NEW);
 SELECT id INTO candidate_override FROM deployment_mcp_readiness_overrides WHERE deployment_id=NEW.id AND policy_revision=(cfg->>'revision')::uuid AND artifact_sha256=subject AND used_at IS NULL AND expires_at>clock_timestamp() ORDER BY created_at DESC LIMIT 1 FOR UPDATE;
 IF candidate_override IS NOT NULL THEN UPDATE deployment_mcp_readiness_overrides SET used_at=clock_timestamp() WHERE id=candidate_override;RETURN NEW; END IF;
 FOR probe_role IN SELECT value FROM jsonb_array_elements(cfg->'roles') LOOP
  IF NOT EXISTS(SELECT 1 FROM deployment_mcp_readiness_evidence e WHERE e.deployment_id=NEW.id AND e.role=probe_role->>'name' AND e.policy_revision=(cfg->>'revision')::uuid AND e.artifact_sha256=subject AND e.passed AND e.observed_at>=clock_timestamp()-make_interval(secs=>(cfg->>'max_age_seconds')::int) AND e.observed_at<=clock_timestamp()) THEN
   RAISE EXCEPTION 'fresh MCP readiness evidence required' USING ERRCODE='23514',CONSTRAINT='deployment_mcp_readiness_required';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
CREATE TRIGGER deployment_mcp_readiness_gate BEFORE INSERT OR UPDATE OF status,traffic_percent ON deployments FOR EACH ROW EXECUTE FUNCTION guard_deployment_mcp_readiness();
END $migration$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM app_mcp_readiness_policy_history) OR EXISTS(SELECT 1 FROM app_mcp_readiness_policies WHERE (policy->>'enabled')::boolean) OR EXISTS(SELECT 1 FROM deployment_mcp_readiness_overrides) OR EXISTS(SELECT 1 FROM deployment_mcp_readiness_evidence) THEN RAISE EXCEPTION 'disable readiness policies and archive retained readiness evidence before rollback'; END IF;
END $$;
DROP TRIGGER deployment_mcp_readiness_gate ON deployments;
DROP FUNCTION guard_deployment_mcp_readiness();
DROP FUNCTION mcp_readiness_subject(deployments);
DROP TABLE deployment_mcp_readiness_overrides,deployment_mcp_readiness_evidence,app_mcp_readiness_policy_history,app_mcp_readiness_policies;
-- +goose StatementEnd
