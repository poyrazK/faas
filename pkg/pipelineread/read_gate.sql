CREATE SCHEMA IF NOT EXISTS gregale_data_api;
REVOKE ALL ON SCHEMA gregale_data_api FROM PUBLIC;

CREATE OR REPLACE FUNCTION gregale_data_api.check_pipeline_read() RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $gate$
DECLARE
 claims jsonb := coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb;
 headers jsonb := coalesce(nullif(current_setting('request.headers',true),''),'{}')::jsonb;
 binding text := current_setting('role',true);
 relation record;
 readable boolean;
BEGIN
 IF binding IS NULL OR binding='none' THEN binding:=session_user; END IF;
 IF coalesce(headers->>'accept-profile','public')<>'public' THEN RETURN; END IF;
 FOR relation IN
  SELECT c.oid,c.relname,nullif(a.tenant_access->>'valid_until','')::timestamptz AS auth_valid_until
  FROM gregale_pipeline.mirrors m
  JOIN gregale_pipeline.reader_access a USING(pipeline_id,generation)
  JOIN pg_class c ON c.relname=ANY(m.tables)
  JOIN pg_namespace n ON n.oid=c.relnamespace AND n.nspname='public'
  WHERE (claims->>'sub'=ANY(a.subjects) OR (coalesce(claims->>'sub','')<>''
   AND nullif(a.tenant_access->>'valid_until','')::timestamptz<=clock_timestamp()))
   AND c.relname IN (
    WITH RECURSIVE requested(name) AS (
     SELECT selected.name FROM unnest(m.tables) selected(name)
      WHERE current_setting('request.path',true)='/'||selected.name
     UNION
     SELECT rel->>'source_table' FROM jsonb_array_elements(m.relationships) rel
      WHERE current_setting('request.path',true)='/rpc/'||(rel->>'name')
    ), dependencies(name) AS (
     SELECT name FROM requested
     UNION
     SELECT rel->>'target_table' FROM dependencies d,jsonb_array_elements(m.relationships) rel
      WHERE rel->>'source_table'=d.name
    ) SELECT name FROM dependencies)
   AND has_table_privilege(binding,c.oid,'SELECT')
   AND obj_description(c.oid,'pg_class') LIKE 'gregale.pipeline.v1:%:'||m.pipeline_id::text||':'||m.generation::text
  ORDER BY c.relname COLLATE "C"
 LOOP
  IF current_setting('transaction_isolation')<>'read committed' THEN
   RAISE SQLSTATE 'PT503' USING MESSAGE='pipeline_read_isolation_unsupported';
  END IF;
  IF relation.auth_valid_until<=clock_timestamp() THEN
   RAISE SQLSTATE 'PT503' USING MESSAGE='pipeline_auth_membership_unconfirmed';
  END IF;
  -- Keep the policy and the subsequent SELECT in the same admission window.
  -- This works for empty tables and PostgREST's read-only GET transactions.
  EXECUTE format('LOCK TABLE public.%I IN ACCESS SHARE MODE',relation.relname);
  SELECT c.relrowsecurity AND coalesce(pg_get_expr(p.polqual,p.polrelid)<>'false',false)
   INTO readable FROM pg_class c LEFT JOIN pg_policy p ON p.polrelid=c.oid AND p.polname='gregale_pipeline_read'
   WHERE c.oid=relation.oid;
  IF readable IS DISTINCT FROM true THEN
   RAISE SQLSTATE 'PGRST'
    USING MESSAGE='{"code":"mirror_unavailable","message":"Mirror reads are temporarily unavailable","details":null,"hint":"Retry after pipeline recovery completes"}',
     DETAIL='{"status":503,"headers":{"Retry-After":"__PIPELINE_RETRY_SECONDS__","Cache-Control":"no-store"}}';
  END IF;
 END LOOP;
END $gate$;

-- Compose with the existing deadline/refresh hooks without changing their
-- pinned bodies or executing an invoker hook with elevated privileges.
CREATE OR REPLACE FUNCTION gregale_data_api.check_pipeline_request() RETURNS void
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $gate$
DECLARE contract jsonb := coalesce(nullif(current_setting('request.jwt.claims',true),''),'{}')::jsonb->'gregale_pipeline_request';
BEGIN
 IF contract->>'version' IS DISTINCT FROM '1' THEN
  RAISE SQLSTATE 'PT503' USING MESSAGE='pipeline_request_contract_unavailable';
 END IF;
 CASE coalesce(contract->>'previous_hook','')
  WHEN '' THEN NULL;
  WHEN 'gregale_data_api.check_deadline_request' THEN PERFORM gregale_data_api.check_deadline_request();
  WHEN 'gregale_data_api.check_request' THEN PERFORM gregale_data_api.check_request();
  ELSE RAISE SQLSTATE 'PT503' USING MESSAGE='pipeline_request_contract_unavailable';
 END CASE;
 PERFORM gregale_data_api.check_pipeline_read();
END $gate$;
REVOKE ALL ON FUNCTION gregale_data_api.check_pipeline_read() FROM PUBLIC;
REVOKE ALL ON FUNCTION gregale_data_api.check_pipeline_request() FROM PUBLIC;
