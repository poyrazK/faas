-- +goose Up
-- +goose StatementBegin
DO $migration$ DECLARE previous jsonb; definition text; BEGIN
 IF to_regprocedure('mcp_readiness_policy_scope_guard_base()') IS NOT NULL THEN RETURN; END IF;
 previous:=jsonb_build_array(pg_get_functiondef('mcp_readiness_subject(deployments)'::regprocedure),pg_get_functiondef('guard_deployment_mcp_readiness()'::regprocedure));
 EXECUTE format('CREATE FUNCTION mcp_readiness_policy_scope_guard_base() RETURNS jsonb LANGUAGE sql IMMUTABLE AS %L',format('SELECT %L::jsonb',previous::text));
 ALTER TABLE app_mcp_readiness_policies ADD COLUMN scope text NOT NULL DEFAULT 'default' CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
 ALTER TABLE app_mcp_readiness_policy_history ADD COLUMN scope text NOT NULL DEFAULT 'default' CHECK(scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$');
 ALTER TABLE app_mcp_readiness_policies DROP CONSTRAINT app_mcp_readiness_policies_pkey;
 ALTER TABLE app_mcp_readiness_policies ADD PRIMARY KEY(app_id,scope);
 ALTER TABLE app_mcp_readiness_policies ADD CONSTRAINT mcp_readiness_policy_scope_matches CHECK(COALESCE(NULLIF(policy->>'scope',''),'default')=scope);
 ALTER TABLE app_mcp_readiness_policy_history ADD CONSTRAINT mcp_readiness_history_scope_matches CHECK(COALESCE(NULLIF(policy->>'scope',''),'default')=scope);
 CREATE INDEX app_mcp_readiness_policy_history_scope ON app_mcp_readiness_policy_history(app_id,scope);
 definition:=previous->>0;
 IF position('WHERE p.app_id=d.app_id AND' IN definition)=0 THEN RAISE EXCEPTION 'unsupported readiness subject'; END IF;
 EXECUTE replace(definition,'WHERE p.app_id=d.app_id AND','WHERE p.app_id=d.app_id AND p.scope=COALESCE(NULLIF(d.scope,''''),''default'') AND');
 definition:=previous->>1;
 IF position('SELECT policy INTO cfg FROM app_mcp_readiness_policies WHERE app_id=NEW.app_id FOR SHARE;' IN definition)=0 OR position('IF cfg IS NULL OR NOT (cfg->>''enabled'')::boolean THEN RETURN NEW; END IF;' IN definition)=0 THEN RAISE EXCEPTION 'unsupported readiness traffic guard'; END IF;
 definition:=replace(definition,'SELECT policy INTO cfg FROM app_mcp_readiness_policies WHERE app_id=NEW.app_id FOR SHARE;',
 'PERFORM pg_advisory_xact_lock_shared(hashtextextended(''mcp-readiness-policy:''||NEW.app_id::text,0)); SELECT policy INTO cfg FROM app_mcp_readiness_policies WHERE app_id=NEW.app_id AND scope=COALESCE(NULLIF(NEW.scope,''''),''default'') FOR SHARE;');
 definition:=replace(definition,'IF cfg IS NULL OR NOT (cfg->>''enabled'')::boolean THEN RETURN NEW; END IF;',
 'IF cfg IS NULL THEN IF COALESCE(NULLIF(NEW.scope,''''),''default'')<>''default'' AND EXISTS(SELECT 1 FROM app_mcp_readiness_policies WHERE app_id=NEW.app_id) THEN RAISE EXCEPTION ''configure MCP readiness for this environment before increasing traffic'' USING ERRCODE=''23514'',CONSTRAINT=''deployment_mcp_readiness_required''; END IF; RETURN NEW; END IF; IF NOT (cfg->>''enabled'')::boolean THEN RETURN NEW; END IF;');
 EXECUTE definition;
END $migration$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 IF EXISTS(SELECT 1 FROM app_mcp_readiness_policies WHERE scope<>'default') OR EXISTS(SELECT 1 FROM app_mcp_readiness_policy_history WHERE scope<>'default') OR EXISTS(SELECT 1 FROM deployment_mcp_readiness_evidence e JOIN deployments d ON d.id=e.deployment_id WHERE d.scope<>'default') OR EXISTS(SELECT 1 FROM deployment_mcp_readiness_overrides o JOIN deployments d ON d.id=o.deployment_id WHERE d.scope<>'default') THEN RAISE EXCEPTION 'archive environment readiness policies, history, evidence and overrides before rollback'; END IF;
 FOR definition IN SELECT jsonb_array_elements_text(mcp_readiness_policy_scope_guard_base()) LOOP EXECUTE definition; END LOOP;
 ALTER TABLE app_mcp_readiness_policies DROP CONSTRAINT app_mcp_readiness_policies_pkey;
 ALTER TABLE app_mcp_readiness_policies DROP COLUMN scope;
 ALTER TABLE app_mcp_readiness_policies ADD PRIMARY KEY(app_id);
 ALTER TABLE app_mcp_readiness_policy_history DROP COLUMN scope;
 DROP FUNCTION mcp_readiness_policy_scope_guard_base();
END $$;
-- +goose StatementEnd
