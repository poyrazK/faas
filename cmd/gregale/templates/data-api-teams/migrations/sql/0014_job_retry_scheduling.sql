SET LOCAL lock_timeout = '5s';

-- Retry scheduling never assumes that a lost batch response rolled back.
-- Each advancement still reconciles the saved import offset and receipt.
-- Mirrors DataAPIBackgroundJobMaxAttempts/RetryBaseSeconds/RetryMaxSeconds.
ALTER TABLE api.team_order_jobs ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
  ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  ADD COLUMN last_error_code text;
ALTER TABLE api.team_order_jobs DROP CONSTRAINT team_order_jobs_state_check,
  DROP CONSTRAINT team_order_jobs_check3;
ALTER TABLE api.team_order_jobs ADD CHECK(state IN ('queued','running','completed','failed','cancelled','blocked')),
  ADD CONSTRAINT team_order_jobs_failure_check CHECK((state IN ('failed','blocked'))=(failure IS NOT NULL));
CREATE INDEX team_order_jobs_due ON api.team_order_jobs(next_attempt_at,created_at,id) WHERE state IN ('queued','running');

CREATE OR REPLACE FUNCTION api.claim_team_order_job() RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; previous text:=current_setting('gregale.order_job_write',true);
BEGIN
  IF current_setting('request.jwt.claims',true)::jsonb->>'gregale_access' IS DISTINCT FROM 'server' OR nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NOT NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_worker_required'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  SELECT * INTO j FROM api.team_order_jobs WHERE state IN ('queued','running') AND next_attempt_at<=clock_timestamp()
    AND (state='queued' OR leased_until<clock_timestamp()) ORDER BY next_attempt_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1;
  IF NOT FOUND THEN PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true); RETURN; END IF;
  IF j.attempts>=8 THEN
    UPDATE api.team_order_jobs SET state='blocked',failure=jsonb_build_object('code',coalesce(last_error_code,'worker_lease_expired'),'uncertain',true,'processed',processed),lease_token=NULL,leased_until=NULL WHERE id=j.id RETURNING * INTO j;
  ELSE
    UPDATE api.team_order_jobs SET state='running',attempts=attempts+1,lease_token=gen_random_uuid(),leased_until=clock_timestamp()+interval '60 seconds',
      next_attempt_at=clock_timestamp()+make_interval(secs=>60+least(300,2*power(2,attempts)::integer)) WHERE id=j.id RETURNING * INTO j;
  END IF;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true); RETURN NEXT j;
END $$;

CREATE FUNCTION api.retry_team_order_job(job_id uuid,claim_token uuid,failure_code text) RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; previous text:=current_setting('gregale.order_job_write',true);
BEGIN
  IF current_setting('request.jwt.claims',true)::jsonb->>'gregale_access' IS DISTINCT FROM 'server' OR nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','') IS NOT NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_worker_required'; END IF;
  IF failure_code IS NULL OR failure_code !~ '^[A-Za-z0-9_]{1,64}$' THEN RAISE SQLSTATE 'PT400' USING MESSAGE='invalid_failure_code'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  -- Waits for an in-flight batch to commit. A completed job or a changed lease
  -- is returned untouched; saved offsets resolve partial progress on retry.
  SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id FOR UPDATE;
  IF j.state='running' AND j.lease_token=claim_token THEN
    UPDATE api.team_order_jobs SET state=CASE WHEN attempts>=8 THEN 'blocked' ELSE 'queued' END,
      failure=CASE WHEN attempts>=8 THEN jsonb_build_object('code',failure_code,'uncertain',true,'processed',processed) END,
      last_error_code=failure_code,lease_token=NULL,leased_until=NULL,
      next_attempt_at=clock_timestamp()+make_interval(secs=>least(300,2*power(2,least(attempts-1,8))::integer)) WHERE id=j.id RETURNING * INTO j;
  END IF;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true); IF j.id IS NOT NULL THEN RETURN NEXT j; END IF;
END $$;

CREATE FUNCTION api.resume_team_order_job(job_id uuid) RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; previous text:=current_setting('gregale.order_job_write',true); caller text:=nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id;
  IF NOT FOUND OR caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_unavailable'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||j.tenant_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members WHERE tenant_id=j.tenant_id AND subject=caller AND (role='owner' OR (role='admin' AND caller=j.actor))) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  UPDATE api.team_order_jobs SET state='queued',attempts=0,failure=NULL,next_attempt_at=clock_timestamp() WHERE id=j.id AND state='blocked' RETURNING * INTO j;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true); IF j.id IS NOT NULL THEN RETURN NEXT j; END IF;
END $$;
REVOKE ALL ON FUNCTION api.retry_team_order_job(uuid,uuid,text),api.resume_team_order_job(uuid) FROM PUBLIC;
COMMENT ON FUNCTION api.retry_team_order_job(uuid,uuid,text) IS '@gregale:rpc';
COMMENT ON FUNCTION api.resume_team_order_job(uuid) IS '@gregale:rpc';

CREATE OR REPLACE FUNCTION api.start_team_order_job(operation_id uuid, team_id text, job_kind text, source_import uuid DEFAULT NULL)
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
  IF (SELECT count(*) FROM api.team_order_jobs WHERE tenant_id=team_id AND actor=caller AND state IN ('queued','running','blocked'))>=16 THEN RAISE SQLSTATE 'PT429' USING MESSAGE='background_job_limit'; END IF;
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

CREATE OR REPLACE FUNCTION api.cancel_team_order_job(job_id uuid) RETURNS SETOF api.team_order_jobs
LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS $$
DECLARE j api.team_order_jobs; previous text:=current_setting('gregale.order_job_write',true); caller text:=nullif(current_setting('request.jwt.claims',true)::jsonb->>'sub','');
BEGIN
  SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id;
  IF NOT FOUND OR caller IS NULL THEN RAISE SQLSTATE '42501' USING MESSAGE='job_unavailable'; END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('gregale:team-admin:'||j.tenant_id,0));
  IF NOT EXISTS(SELECT 1 FROM api.team_members WHERE tenant_id=j.tenant_id AND subject=caller AND (role='owner' OR (role='admin' AND caller=j.actor))) THEN RAISE SQLSTATE '42501' USING MESSAGE='team_write_denied'; END IF;
  PERFORM set_config('gregale.order_job_write','on',true);
  UPDATE api.team_order_jobs SET state='cancelled',failure=NULL,lease_token=NULL,leased_until=NULL,completed_at=clock_timestamp() WHERE id=j.id AND state IN ('queued','running','blocked') RETURNING * INTO j;
  IF NOT FOUND THEN SELECT * INTO j FROM api.team_order_jobs WHERE id=job_id; END IF;
  PERFORM set_config('gregale.order_job_write',coalesce(previous,''),true);
  RETURN NEXT j;
END $$;

