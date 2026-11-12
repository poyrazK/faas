-- Optional background import/export adapter for scheduled Gregale Jobs.
-- Application limits: 16 active jobs per actor and tenant, 10,000 export rows / 10 MiB.
CREATE TABLE api.team_order_jobs (
  id uuid PRIMARY KEY,
  tenant_id text NOT NULL,
  actor text NOT NULL,
  kind text NOT NULL CHECK(kind IN ('import','export')),
  import_id uuid,
  state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','running','completed','failed','cancelled')),
  processed integer NOT NULL DEFAULT 0 CHECK(processed>=0),
  total integer NOT NULL DEFAULT 0 CHECK(total>=processed),
  result jsonb,
  failure jsonb,
  lease_token uuid,
  leased_until timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  CHECK((kind='import')=(import_id IS NOT NULL)),
  CHECK((state='completed')=(result IS NOT NULL)),
  CHECK((state='failed')=(failure IS NOT NULL))
);
CREATE INDEX team_order_jobs_queue ON api.team_order_jobs(created_at,id) WHERE state IN ('queued','running');
ALTER TABLE api.team_order_jobs ENABLE ROW LEVEL SECURITY;
CREATE POLICY job_reads ON api.team_order_jobs FOR SELECT USING (
  (current_setting('request.jwt.claims',true)::jsonb->>'gregale_access'='server' AND nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NULL)
  OR EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_jobs.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND (m.role='owner' OR (m.role='admin' AND actor=m.subject)))
);
CREATE POLICY job_creates ON api.team_order_jobs FOR INSERT WITH CHECK (
  current_setting('gregale.order_job_write',true)='on' AND actor=current_setting('request.jwt.claims',true)::jsonb->>'sub'
);
CREATE POLICY job_updates ON api.team_order_jobs FOR UPDATE USING (
  current_setting('gregale.order_job_write',true)='on' AND (
    (current_setting('request.jwt.claims',true)::jsonb->>'gregale_access'='server' AND nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NULL)
    OR EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=team_order_jobs.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub' AND (m.role='owner' OR (m.role='admin' AND actor=m.subject)))
  )
) WITH CHECK(current_setting('gregale.order_job_write',true)='on');

CREATE FUNCTION api.start_team_order_job(operation_id uuid, team_id text, job_kind text, source_import uuid DEFAULT NULL)
RETURNS SETOF api.team_order_jobs LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; source api.team_order_imports; previous text:=current_setting('gregale.order_job_write',true); caller text:=nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  IF operation_id IS NULL OR caller IS NULL OR octet_length(caller)>255 OR caller ~ '[[:cntrl:]]' OR job_kind IS NULL OR job_kind NOT IN ('import','export') OR ((job_kind='import')<>(source_import IS NOT NULL)) THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_background_job'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||team_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members WHERE tenant_id=team_id AND subject=caller AND role IN ('owner','admin')) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  SELECT * INTO j FROM api.team_order_jobs WHERE id=operation_id;
  IF FOUND THEN
    IF (j.tenant_id,j.actor,j.kind,j.import_id) IS DISTINCT FROM (team_id,caller,job_kind,source_import) THEN RAISE SQLSTATE 'PT409' USING MESSAGE='job_payload_conflict'; END IF;
    RETURN NEXT j; RETURN;
  END IF;
  IF (SELECT count(*) FROM api.team_order_jobs WHERE tenant_id=team_id AND actor=caller AND state IN ('queued','running'))>=16 THEN RAISE SQLSTATE 'PT429' USING MESSAGE='background_job_limit'; END IF;
  IF job_kind='import' THEN
    SELECT * INTO source FROM api.team_order_imports WHERE import_id=source_import AND tenant_id=team_id AND actor=caller;
    IF NOT FOUND THEN RAISE SQLSTATE 'PT409' USING MESSAGE='import_unavailable'; END IF;
  END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  INSERT INTO api.team_order_jobs(id,tenant_id,actor,kind,import_id,total,processed)
    VALUES(operation_id,team_id,caller,job_kind,source_import,coalesce(source.total,0),coalesce(source.processed,0)) RETURNING * INTO j;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true);
  RETURN NEXT j;
END $$;

-- Only a backend server key without subject can lease work. The lease is
-- generated in PostgreSQL, and contains no reusable application credential.
CREATE FUNCTION api.claim_team_order_job() RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; previous text:=current_setting('gregale.order_job_write',true);
BEGIN
  IF current_setting('request.jwt.claims',true)::jsonb->>'gregale_access' IS DISTINCT FROM 'server' OR nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NOT NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_worker_required'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  SELECT * INTO j FROM api.team_order_jobs WHERE state='queued' OR (state='running' AND leased_until<clock_timestamp()) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
  IF NOT FOUND THEN PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true); RETURN; END IF;
  UPDATE api.team_order_jobs SET state='running',lease_token=gen_random_uuid(),leased_until=clock_timestamp()+interval '60 seconds' WHERE id=j.id RETURNING * INTO j;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true);
  RETURN NEXT j;
END $$;

CREATE FUNCTION api.advance_team_order_job(job_id uuid, claim_token uuid) RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; source api.team_order_imports; exported jsonb; previous text:=current_setting('gregale.order_job_write',true); caller text:=nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  IF current_setting('request.jwt.claims',true)::jsonb->>'gregale_access' IS DISTINCT FROM 'server' OR caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_worker_required'; END IF;
  -- Always take the membership lock before the job lock, as cancellation and
  -- import advancement do. Revocations serialize with each bounded batch.
  SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id;
  IF NOT FOUND OR j.actor<>caller THEN RAISE SQLSTATE '42501' USING MESSAGE='job_actor_required'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||j.tenant_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members WHERE tenant_id=j.tenant_id AND subject=caller AND role IN ('owner','admin')) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id FOR UPDATE;
  IF j.state='completed' THEN PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true); RETURN NEXT j; RETURN; END IF;
  IF j.state IS DISTINCT FROM 'running' OR j.lease_token IS DISTINCT FROM claim_token OR j.leased_until<=clock_timestamp() THEN RAISE SQLSTATE 'PT409' USING MESSAGE='job_lease_unavailable'; END IF;
  IF j.kind='import' THEN
    SELECT * INTO source FROM api.advance_team_order_import_once(j.import_id,j.tenant_id,j.processed);
    UPDATE api.team_order_jobs SET processed=source.processed,total=source.total,
      state=CASE WHEN source.completed_at IS NULL THEN 'running' ELSE 'completed' END,
      result=CASE WHEN source.completed_at IS NOT NULL THEN to_jsonb(source)-'manifest' END,
      completed_at=source.completed_at,leased_until=clock_timestamp()+interval '60 seconds' WHERE id=j.id RETURNING * INTO j;
  ELSE
    -- One SELECT materializes a consistent PostgreSQL statement snapshot.
    -- Subsequent downloads read these saved bytes, never a changing table.
    SELECT coalesce(jsonb_agg(to_jsonb(o) ORDER BY o.id),'[]'::jsonb) INTO exported FROM
      (SELECT * FROM api.team_orders WHERE tenant_id=j.tenant_id ORDER BY id LIMIT 10001) o;
    IF jsonb_array_length(exported)>10000 OR octet_length(exported::text)>10485760 THEN RAISE SQLSTATE 'PT413' USING MESSAGE='export_limit'; END IF;
    UPDATE api.team_order_jobs SET state='completed',processed=jsonb_array_length(exported),total=jsonb_array_length(exported),
      result=jsonb_build_object('snapshot_at',statement_timestamp(),'rows',exported),completed_at=clock_timestamp() WHERE id=j.id RETURNING * INTO j;
  END IF;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true);
  RETURN NEXT j;
END $$;

CREATE FUNCTION api.fail_team_order_job(job_id uuid, claim_token uuid, failure_code text) RETURNS void
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE previous text:=current_setting('gregale.order_job_write',true);
BEGIN
  IF current_setting('request.jwt.claims',true)::jsonb->>'gregale_access' IS DISTINCT FROM 'server' OR nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NOT NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_worker_required'; END IF;
  IF failure_code IS NULL OR failure_code !~ '^[A-Za-z0-9_]{1,64}$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_failure_code'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  UPDATE api.team_order_jobs SET state='failed',failure=CASE WHEN kind='import' THEN jsonb_build_object('code',failure_code,'first_row',processed+1,'last_row',least(total,processed+20)) ELSE jsonb_build_object('code',failure_code) END,completed_at=clock_timestamp()
    WHERE id=job_id AND state='running' AND lease_token=claim_token AND leased_until>clock_timestamp();
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true);
END $$;

CREATE FUNCTION api.cancel_team_order_job(job_id uuid) RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; previous text:=current_setting('gregale.order_job_write',true); caller text:=nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id;
  IF NOT FOUND OR caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_unavailable'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||j.tenant_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members WHERE tenant_id=j.tenant_id AND subject=caller AND (role='owner' OR (role='admin' AND caller=j.actor))) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  UPDATE api.team_order_jobs SET state='cancelled',completed_at=clock_timestamp() WHERE id=j.id AND state IN ('queued','running') RETURNING * INTO j;
  IF NOT FOUND THEN SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id; END IF;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true);
  RETURN NEXT j;
END $$;

REVOKE ALL ON FUNCTION api.start_team_order_job(uuid,text,text,uuid),api.claim_team_order_job(),api.advance_team_order_job(uuid,uuid),api.fail_team_order_job(uuid,uuid,text),api.cancel_team_order_job(uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.start_team_order_job(uuid,text,text,uuid) IS '@gregale:rpc';
COMMENT ON FUNCTION api.claim_team_order_job() IS '@gregale:rpc';
COMMENT ON FUNCTION api.advance_team_order_job(uuid,uuid) IS '@gregale:rpc';
COMMENT ON FUNCTION api.fail_team_order_job(uuid,uuid,text) IS '@gregale:rpc';
COMMENT ON FUNCTION api.cancel_team_order_job(uuid) IS '@gregale:rpc';
