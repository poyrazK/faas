-- +goose Up
-- +goose StatementBegin
DO $migration$ DECLARE original_guard text; subject_schema text; BEGIN
 IF to_regprocedure('mcp_readiness_runtime_subject(deployments)') IS NOT NULL THEN RETURN; END IF;
 SELECT n.nspname INTO STRICT subject_schema FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid='mcp_readiness_subject(deployments)'::regprocedure;
 EXECUTE replace(pg_get_functiondef('mcp_readiness_subject(deployments)'::regprocedure),format('FUNCTION %I.mcp_readiness_subject(',subject_schema),format('FUNCTION %I.mcp_readiness_runtime_subject(',subject_schema));
 original_guard:=pg_get_functiondef('guard_deployment_mcp_readiness()'::regprocedure);
 IF position('subject:=mcp_readiness_subject(NEW);' IN original_guard)=0 THEN RAISE EXCEPTION 'unsupported readiness guard definition'; END IF;
 EXECUTE format('CREATE FUNCTION mcp_readiness_oauth_guard_base() RETURNS text LANGUAGE sql IMMUTABLE AS %L',format('SELECT %L',original_guard));
 EXECUTE replace(original_guard,'subject:=mcp_readiness_subject(NEW);',
 'IF EXISTS(SELECT 1 FROM jsonb_array_elements(cfg->''roles'') r WHERE r#>''{monitor,oauth_connection}'' IS NOT NULL AND r#>''{monitor,oauth_connection}''<>''null''::jsonb) THEN PERFORM app_id FROM mcp_catalog_monitors WHERE app_id=NEW.app_id FOR SHARE; END IF; subject:=mcp_readiness_subject(NEW);');
EXECUTE $definition$CREATE OR REPLACE FUNCTION mcp_readiness_subject(d deployments) RETURNS text LANGUAGE sql STABLE AS $$
 WITH bindings AS (
 SELECT jsonb_agg(jsonb_build_array(role.value->>'name',
   connection.view#>'{config,revision}', connection.view#>'{config,enabled}',
   connection.view#>'{config,observer,enabled}',connection.view#>'{config,observer,paused}',connection.view#>'{config,observer,endpoint}',
   connection.view#>'{config,observer,oauth}' IS NOT NULL AND connection.view#>'{config,observer,oauth}'<>'null'::jsonb,
   connection.view#>'{observer_status,oauth,state}',connection.view#>'{observer_status,oauth,source_sha256}',
   connection.view#>'{observer_status,oauth,retirement_id}',connection.view#>'{observer_status,oauth,granted_scopes}',
   connection.view#>'{observer_status,oauth_flow,state_sha256}',connection.view#>'{observer_status,oauth_flow,consumed}',
   connection.view#>'{oauth_lifecycle,credential_state}',connection.view#>'{oauth_lifecycle,local_state}') ORDER BY role.value->>'name') AS value
 FROM app_mcp_readiness_policies p CROSS JOIN LATERAL jsonb_array_elements(p.policy->'roles') AS role(value)
 LEFT JOIN mcp_catalog_monitors m ON m.app_id=p.app_id AND m.account_id=(SELECT account_id FROM apps WHERE id=p.app_id)
 LEFT JOIN LATERAL (SELECT CASE WHEN role.value#>>'{monitor,oauth_connection,role}'='default' THEN m.payload ELSE m.payload->'roles'->(role.value#>>'{monitor,oauth_connection,role}') END AS view) connection ON true
 WHERE p.app_id=d.app_id AND role.value#>'{monitor,oauth_connection}' IS NOT NULL AND role.value#>'{monitor,oauth_connection}'<>'null'::jsonb)
 SELECT CASE WHEN bindings.value IS NULL THEN mcp_readiness_runtime_subject(d)
 ELSE encode(sha256(convert_to(jsonb_build_array(mcp_readiness_runtime_subject(d),bindings.value)::text,'UTF8')),'hex') END FROM bindings
$$;$definition$;
END $migration$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE subject_schema text; BEGIN
 IF EXISTS(SELECT 1 FROM app_mcp_readiness_policies p CROSS JOIN LATERAL jsonb_array_elements(p.policy->'roles') AS role(value) WHERE role.value#>'{monitor,oauth_connection}' IS NOT NULL AND role.value#>'{monitor,oauth_connection}'<>'null'::jsonb) THEN RAISE EXCEPTION 'remove readiness OAuth connection references before rollback'; END IF;
 EXECUTE mcp_readiness_oauth_guard_base();
 SELECT n.nspname INTO STRICT subject_schema FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid='mcp_readiness_runtime_subject(deployments)'::regprocedure;
 EXECUTE replace(pg_get_functiondef('mcp_readiness_runtime_subject(deployments)'::regprocedure),format('FUNCTION %I.mcp_readiness_runtime_subject(',subject_schema),format('FUNCTION %I.mcp_readiness_subject(',subject_schema));
END $$;
DROP FUNCTION mcp_readiness_oauth_guard_base();
DROP FUNCTION mcp_readiness_runtime_subject(deployments);
-- +goose StatementEnd
