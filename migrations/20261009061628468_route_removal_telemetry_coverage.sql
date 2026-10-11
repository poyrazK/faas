-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS request_telemetry_coverage (
 node_name text PRIMARY KEY REFERENCES compute_nodes(name) ON DELETE CASCADE CHECK(node_name<>''),
 boot_id uuid NOT NULL CHECK(boot_id<>'00000000-0000-0000-0000-000000000000'),
 sequence bigint NOT NULL CHECK(sequence>0),
 enabled boolean NOT NULL,
 sampling_basis_points integer NOT NULL CHECK(sampling_basis_points BETWEEN 0 AND 10000),
 dropped_total bigint NOT NULL CHECK(dropped_total>=0),
 pending_count integer NOT NULL CHECK(pending_count>=0),
 source_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL,
 healthy_since timestamptz,
 CHECK(healthy_since IS NULL OR healthy_since<=received_at)
);

CREATE OR REPLACE FUNCTION record_telemetry_coverage(node text, boot uuid, seq bigint, is_enabled boolean, sampling integer, drops bigint, pending integer, source_stamp timestamptz) RETURNS void LANGUAGE plpgsql AS $$
DECLARE stamp timestamptz:=clock_timestamp();
BEGIN
 IF source_stamp>stamp+interval '5 seconds' OR source_stamp<stamp-interval '30 seconds' THEN
 RAISE EXCEPTION 'telemetry coverage clock is stale or in the future' USING ERRCODE='22023'; END IF;
 INSERT INTO request_telemetry_coverage AS old(node_name,boot_id,sequence,enabled,sampling_basis_points,dropped_total,pending_count,source_at,received_at,healthy_since)
 VALUES(node,boot,seq,is_enabled,sampling,drops,pending,source_stamp,stamp,
 CASE WHEN is_enabled AND sampling=10000 AND drops=0 AND pending=0 THEN stamp END)
 ON CONFLICT(node_name) DO UPDATE SET
 boot_id=excluded.boot_id,sequence=excluded.sequence,enabled=excluded.enabled,sampling_basis_points=excluded.sampling_basis_points,
 dropped_total=excluded.dropped_total,pending_count=excluded.pending_count,source_at=excluded.source_at,received_at=excluded.received_at,
 healthy_since=CASE WHEN NOT excluded.enabled OR excluded.sampling_basis_points<>10000 OR excluded.pending_count<>0 THEN NULL
 WHEN old.boot_id<>excluded.boot_id OR old.received_at<stamp-interval '30 seconds' OR old.dropped_total<>excluded.dropped_total THEN stamp
 ELSE coalesce(old.healthy_since,stamp) END
 WHERE excluded.source_at>old.source_at AND (old.boot_id<>excluded.boot_id OR excluded.sequence>old.sequence);
 IF NOT FOUND THEN RAISE EXCEPTION 'telemetry coverage replay or out-of-order heartbeat' USING ERRCODE='22023'; END IF;
END $$;

-- Hold membership and heartbeat rows through a traffic/approval transaction.
-- The heartbeat writer never acquires app locks, avoiding a lock cycle.
CREATE OR REPLACE FUNCTION lock_route_removal_coverage() RETURNS void LANGUAGE plpgsql AS $$
BEGIN
 PERFORM 1 FROM compute_nodes ORDER BY name FOR SHARE;
 PERFORM 1 FROM request_telemetry_coverage ORDER BY node_name FOR SHARE;
END $$;

CREATE OR REPLACE FUNCTION route_removal_coverage_blockers(window_from timestamptz, window_until timestamptz) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE blockers jsonb:='[]'; stamp timestamptz:=clock_timestamp();
BEGIN
 IF NOT EXISTS(SELECT 1 FROM compute_nodes WHERE active) OR EXISTS(SELECT 1 FROM compute_nodes n LEFT JOIN request_telemetry_coverage c ON c.node_name=n.name WHERE n.active AND c.node_name IS NULL) THEN
 blockers:=blockers||jsonb_build_array('telemetry_coverage_missing'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND c.received_at<stamp-interval '30 seconds') THEN
 blockers:=blockers||jsonb_build_array('telemetry_coverage_stale'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND NOT c.enabled) THEN
 blockers:=blockers||jsonb_build_array('telemetry_disabled'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND c.sampling_basis_points<>10000) THEN
 blockers:=blockers||jsonb_build_array('telemetry_sampled'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND (c.pending_count>0 OR c.source_at<window_until)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_ingestion_pending'); END IF;
 IF EXISTS(SELECT 1 FROM request_telemetry_coverage c JOIN compute_nodes n ON n.name=c.node_name WHERE n.active AND (c.healthy_since IS NULL OR c.healthy_since>window_from)) THEN
 blockers:=blockers||jsonb_build_array('telemetry_window_incomplete'); END IF;
 RETURN blockers;
END $$;

CREATE OR REPLACE FUNCTION route_removal_check(target_id uuid, proposed_scope text DEFAULT NULL, proposed_app_id uuid DEFAULT NULL) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE p app_route_removal_policies%ROWTYPE; d deployments%ROWTYPE;
 b deployment_openapi_docs%ROWTYPE; c deployment_openapi_docs%ROWTYPE;
 approval route_removal_approvals%ROWTYPE; removed jsonb := '[]'; blockers jsonb := '[]'; stamp timestamptz := clock_timestamp(); earliest timestamptz;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=target_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'deployment not found'; END IF;
 IF proposed_scope IS NOT NULL THEN d.scope:=proposed_scope; END IF;
 IF proposed_app_id IS NOT NULL THEN d.app_id:=proposed_app_id; END IF;
 SELECT * INTO p FROM app_route_removal_policies WHERE app_id=d.app_id;
 IF NOT FOUND THEN RETURN jsonb_build_object('status','not_configured','removed',removed,'blockers',blockers); END IF;
 IF coalesce(nullif(d.scope,''),'default') NOT IN ('default','prod','production') OR p.baseline_deployment_id IS NULL OR p.baseline_deployment_id=d.id THEN
 RETURN jsonb_build_object('status','not_required','removed',removed,'blockers',blockers); END IF;
 SELECT * INTO b FROM deployment_openapi_docs WHERE deployment_id=p.baseline_deployment_id AND app_id=d.app_id AND account_id=p.account_id;
 SELECT * INTO c FROM deployment_openapi_docs WHERE deployment_id=d.id AND app_id=d.app_id AND account_id=p.account_id;
 IF b.deployment_id IS NULL OR c.deployment_id IS NULL OR b.truncated OR c.truncated OR
 coalesce(b.doc->>'openapi','') NOT LIKE '3.%' OR coalesce(c.doc->>'openapi','') NOT LIKE '3.%' OR
 jsonb_typeof(b.doc->'paths') IS DISTINCT FROM 'object' OR jsonb_typeof(c.doc->'paths') IS DISTINCT FROM 'object' THEN
 blockers := jsonb_build_array('contract_unavailable');
 ELSIF EXISTS(SELECT 1 FROM jsonb_each(b.doc->'paths') x WHERE jsonb_typeof(x.value)<>'object' OR x.value ? '$ref' OR x.key NOT LIKE '/%') OR
 EXISTS(SELECT 1 FROM jsonb_each(c.doc->'paths') x WHERE jsonb_typeof(x.value)<>'object' OR x.value ? '$ref' OR x.key NOT LIKE '/%') THEN
 blockers := jsonb_build_array('path_reference_or_invalid_item');
 ELSIF EXISTS(SELECT 1 FROM jsonb_each(b.doc->'paths') item CROSS JOIN LATERAL jsonb_each(item.value) o WHERE lower(o.key) IN ('get','put','post','delete','options','head','patch','trace') AND jsonb_typeof(o.value)<>'object') OR
 EXISTS(SELECT 1 FROM jsonb_each(c.doc->'paths') item CROSS JOIN LATERAL jsonb_each(item.value) o WHERE lower(o.key) IN ('get','put','post','delete','options','head','patch','trace') AND jsonb_typeof(o.value)<>'object') THEN
 blockers:=jsonb_build_array('invalid_operation');
 ELSE
 SELECT coalesce(jsonb_agg(jsonb_build_object('method',x.method,'path',x.path) ORDER BY x.method,x.path),'[]') INTO removed
 FROM (SELECT * FROM route_removal_operations(b.doc) EXCEPT SELECT * FROM route_removal_operations(c.doc)) x;
 IF jsonb_array_length(removed)>0 THEN
 earliest:=date_trunc('minute',greatest(p.baseline_since,b.captured_at))+make_interval(secs=>p.grace_seconds)+interval '3 minutes';
 blockers:=blockers||route_removal_coverage_blockers(date_trunc('minute',stamp-interval '2 minutes')-make_interval(secs=>p.grace_seconds),date_trunc('minute',stamp-interval '2 minutes'));
 SELECT greatest(earliest,date_trunc('minute',max(healthy_since))+make_interval(secs=>p.grace_seconds)+interval '3 minutes') INTO earliest FROM request_telemetry_coverage cv JOIN compute_nodes n ON n.name=cv.node_name WHERE n.active;
 IF stamp < earliest THEN
 blockers:=blockers||jsonb_build_array('grace_period_not_elapsed'); END IF;
 SELECT * INTO approval FROM route_removal_approvals a WHERE a.app_id=p.app_id AND a.account_id=p.account_id AND
 a.policy_revision=p.revision AND a.baseline_deployment_id=p.baseline_deployment_id AND a.candidate_deployment_id=d.id AND
 a.baseline_sha256=b.doc_sha256 AND a.candidate_sha256=c.doc_sha256 AND p.baseline_since<=a.observation_from AND a.valid_until>stamp AND
 a.approved_at>=stamp-make_interval(secs=>p.max_approval_age_seconds) AND
 a.observation_from<=a.observation_until-make_interval(secs=>p.grace_seconds) AND
 a.observation_until>=stamp-make_interval(secs=>p.max_approval_age_seconds)-interval '3 minutes' AND
 NOT EXISTS (SELECT 1 FROM jsonb_array_elements(removed) r WHERE NOT EXISTS
 (SELECT 1 FROM jsonb_array_elements(a.mappings) m WHERE m->>'method'=r->>'method' AND m->>'path'=r->>'path'))
 ORDER BY a.approved_at DESC LIMIT 1;
 IF NOT FOUND THEN blockers:=blockers||jsonb_build_array('fresh_authenticated_approval_required');
 ELSE
 blockers:=blockers||route_removal_coverage_blockers(approval.observation_from,date_trunc('minute',stamp-interval '2 minutes'));
 IF EXISTS (SELECT 1 FROM request_telemetry rt JOIN deployments td ON td.id=rt.deployment_id
 JOIN jsonb_array_elements(removed) r ON rt.method=r->>'method' AND rt.route IN (r->>'path',(r->>'method')||' '||(r->>'path'))
 WHERE rt.app_id=p.app_id AND rt.account_id=p.account_id AND coalesce(nullif(td.scope,''),'default') IN ('default','prod','production')
 AND rt.received_at>=approval.observation_from AND rt.count>0) THEN
 blockers:=blockers||jsonb_build_array('old_route_observed'); END IF;
 END IF;
 END IF;
 END IF;
 SELECT coalesce(jsonb_agg(DISTINCT x),'[]') INTO blockers FROM jsonb_array_elements(blockers) x;
 RETURN jsonb_strip_nulls(jsonb_build_object('status',CASE WHEN jsonb_array_length(blockers)>0 THEN 'blocked' WHEN jsonb_array_length(removed)>0 THEN 'passed' ELSE 'not_required' END,
 'removed',removed,'blockers',blockers,'approval_id',coalesce(approval.id::text,''),
 'earliest_approval_at',earliest,'approval_valid_until',approval.valid_until,
 'baseline_contract_sha256',encode(b.doc_sha256,'hex'),'candidate_contract_sha256',encode(c.doc_sha256,'hex')));
END $$;

CREATE OR REPLACE FUNCTION guard_route_removal_traffic() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p app_route_removal_policies%ROWTYPE; result jsonb; fence jsonb;
BEGIN
 IF NEW.status<>'live' OR NEW.traffic_percent<=0 OR
 (TG_OP='UPDATE' AND OLD.status='live' AND NEW.traffic_percent<=OLD.traffic_percent AND NEW.scope IS NOT DISTINCT FROM OLD.scope AND NEW.app_id=OLD.app_id) THEN RETURN NEW; END IF;
 -- Policy setters and deployment activation already take the app lock. This
 -- additionally fences direct SQL/worker/canary transitions with the policy.
 PERFORM 1 FROM apps WHERE id=NEW.app_id FOR UPDATE;
 SELECT * INTO p FROM app_route_removal_policies WHERE app_id=NEW.app_id;
 SELECT value INTO fence FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.route_removal_fences',true),''),'[]')::jsonb)
 WHERE value->>'deployment_id'=NEW.id::text LIMIT 1;
 -- Captures update the policy observation clock after locking their document.
 -- Keep that document-before-policy order while holding the application lock.
 IF fence IS NOT NULL THEN
 PERFORM 1 FROM deployment_openapi_docs WHERE deployment_id IN (NEW.id,p.baseline_deployment_id) ORDER BY deployment_id FOR SHARE;
 PERFORM 1 FROM deployment_openapi_snapshots WHERE deployment_id IN (NEW.id,p.baseline_deployment_id) ORDER BY deployment_id FOR SHARE;
 END IF;
 SELECT * INTO p FROM app_route_removal_policies WHERE app_id=NEW.app_id FOR UPDATE;
 IF coalesce(nullif(NEW.scope,''),'default') NOT IN ('default','prod','production') THEN RETURN NEW; END IF;
 IF NOT FOUND THEN
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.route_removal_fences',true),''),'[]')::jsonb) WHERE value->>'deployment_id'=NEW.id::text) THEN
 RAISE EXCEPTION 'route removal policy disappeared after contract preflight' USING ERRCODE='23514',CONSTRAINT='route_removal_required'; END IF;
 RETURN NEW; END IF;
 PERFORM lock_route_removal_coverage();
 result:=route_removal_check(NEW.id,coalesce(NEW.scope,''),NEW.app_id);
 SELECT value INTO fence FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.route_removal_fences',true),''),'[]')::jsonb)
 WHERE value->>'deployment_id'=NEW.id::text LIMIT 1;
 IF fence IS NOT NULL AND (p.mode<>'enforce' OR result->>'status'<>'passed' OR
 p.revision::text IS DISTINCT FROM fence->>'policy_revision' OR
 p.baseline_deployment_id::text IS DISTINCT FROM fence->>'baseline_deployment_id' OR
 result->>'approval_id' IS DISTINCT FROM fence->>'approval_id' OR
 result->>'baseline_contract_sha256' IS DISTINCT FROM fence->>'baseline_sha256' OR
 result->>'candidate_contract_sha256' IS DISTINCT FROM fence->>'candidate_sha256') THEN
 RAISE EXCEPTION 'route removal approval changed after contract preflight' USING ERRCODE='23514',CONSTRAINT='route_removal_required';
 END IF;
 IF fence IS NOT NULL AND (
 (coalesce(fence->>'baseline_snapshot_sha256','')<>'' AND NOT EXISTS(SELECT 1 FROM deployment_openapi_snapshots WHERE deployment_id=p.baseline_deployment_id AND sha256=fence->>'baseline_snapshot_sha256')) OR
 (coalesce(fence->>'candidate_snapshot_sha256','')<>'' AND (EXISTS(SELECT 1 FROM deployment_openapi_snapshots WHERE deployment_id=NEW.id AND sha256 IS DISTINCT FROM fence->>'candidate_snapshot_sha256') OR (TG_OP='UPDATE' AND OLD.status='live' AND NOT EXISTS(SELECT 1 FROM deployment_openapi_snapshots WHERE deployment_id=NEW.id AND sha256=fence->>'candidate_snapshot_sha256'))))) THEN
 RAISE EXCEPTION 'route removal contract snapshot changed after preflight' USING ERRCODE='23514',CONSTRAINT='route_removal_required'; END IF;
 IF p.mode='enforce' AND result->>'status'='blocked' THEN
 RAISE EXCEPTION 'route removal blocked' USING ERRCODE='23514',CONSTRAINT='route_removal_required',DETAIL=result::text;
 END IF;
 -- Keep a durable baseline even if callers zero/supersede its row first.
 -- Partial canaries keep the original baseline until a full cutover.
 IF NEW.traffic_percent=100 AND p.baseline_deployment_id IS DISTINCT FROM NEW.id THEN
 UPDATE app_route_removal_policies SET baseline_deployment_id=NEW.id,baseline_since=clock_timestamp() WHERE app_id=NEW.app_id;
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION route_removal_check(target_id uuid, proposed_scope text DEFAULT NULL, proposed_app_id uuid DEFAULT NULL) RETURNS jsonb LANGUAGE plpgsql AS $$
DECLARE p app_route_removal_policies%ROWTYPE; d deployments%ROWTYPE;
 b deployment_openapi_docs%ROWTYPE; c deployment_openapi_docs%ROWTYPE;
 approval route_removal_approvals%ROWTYPE; removed jsonb := '[]'; blockers jsonb := '[]'; stamp timestamptz := clock_timestamp(); earliest timestamptz;
BEGIN
 SELECT * INTO d FROM deployments WHERE id=target_id;
 IF NOT FOUND THEN RAISE EXCEPTION 'deployment not found'; END IF;
 IF proposed_scope IS NOT NULL THEN d.scope:=proposed_scope; END IF;
 IF proposed_app_id IS NOT NULL THEN d.app_id:=proposed_app_id; END IF;
 SELECT * INTO p FROM app_route_removal_policies WHERE app_id=d.app_id;
 IF NOT FOUND THEN RETURN jsonb_build_object('status','not_configured','removed',removed,'blockers',blockers); END IF;
 IF coalesce(nullif(d.scope,''),'default') NOT IN ('default','prod','production') OR p.baseline_deployment_id IS NULL OR p.baseline_deployment_id=d.id THEN
 RETURN jsonb_build_object('status','not_required','removed',removed,'blockers',blockers); END IF;
 SELECT * INTO b FROM deployment_openapi_docs WHERE deployment_id=p.baseline_deployment_id AND app_id=d.app_id AND account_id=p.account_id;
 SELECT * INTO c FROM deployment_openapi_docs WHERE deployment_id=d.id AND app_id=d.app_id AND account_id=p.account_id;
 IF b.deployment_id IS NULL OR c.deployment_id IS NULL OR b.truncated OR c.truncated OR
 coalesce(b.doc->>'openapi','') NOT LIKE '3.%' OR coalesce(c.doc->>'openapi','') NOT LIKE '3.%' OR
 jsonb_typeof(b.doc->'paths') IS DISTINCT FROM 'object' OR jsonb_typeof(c.doc->'paths') IS DISTINCT FROM 'object' THEN
 blockers := jsonb_build_array('contract_unavailable');
 ELSIF EXISTS(SELECT 1 FROM jsonb_each(b.doc->'paths') x WHERE jsonb_typeof(x.value)<>'object' OR x.value ? '$ref' OR x.key NOT LIKE '/%') OR
 EXISTS(SELECT 1 FROM jsonb_each(c.doc->'paths') x WHERE jsonb_typeof(x.value)<>'object' OR x.value ? '$ref' OR x.key NOT LIKE '/%') THEN
 blockers := jsonb_build_array('path_reference_or_invalid_item');
 ELSIF EXISTS(SELECT 1 FROM jsonb_each(b.doc->'paths') item CROSS JOIN LATERAL jsonb_each(item.value) o WHERE lower(o.key) IN ('get','put','post','delete','options','head','patch','trace') AND jsonb_typeof(o.value)<>'object') OR
 EXISTS(SELECT 1 FROM jsonb_each(c.doc->'paths') item CROSS JOIN LATERAL jsonb_each(item.value) o WHERE lower(o.key) IN ('get','put','post','delete','options','head','patch','trace') AND jsonb_typeof(o.value)<>'object') THEN
 blockers:=jsonb_build_array('invalid_operation');
 ELSE
 SELECT coalesce(jsonb_agg(jsonb_build_object('method',x.method,'path',x.path) ORDER BY x.method,x.path),'[]') INTO removed
 FROM (SELECT * FROM route_removal_operations(b.doc) EXCEPT SELECT * FROM route_removal_operations(c.doc)) x;
 IF jsonb_array_length(removed)>0 THEN
 earliest:=greatest(p.baseline_since,b.captured_at)+make_interval(secs=>p.grace_seconds);
 IF stamp < earliest THEN
 blockers:=blockers||jsonb_build_array('grace_period_not_elapsed'); END IF;
 SELECT * INTO approval FROM route_removal_approvals a WHERE a.app_id=p.app_id AND a.account_id=p.account_id AND
 a.policy_revision=p.revision AND a.baseline_deployment_id=p.baseline_deployment_id AND a.candidate_deployment_id=d.id AND
 a.baseline_sha256=b.doc_sha256 AND a.candidate_sha256=c.doc_sha256 AND p.baseline_since<=a.observation_from AND a.valid_until>stamp AND
 a.approved_at>=stamp-make_interval(secs=>p.max_approval_age_seconds) AND
 a.observation_from<=a.observation_until-make_interval(secs=>p.grace_seconds) AND
 a.observation_until>=stamp-make_interval(secs=>p.max_approval_age_seconds) AND
 NOT EXISTS (SELECT 1 FROM jsonb_array_elements(removed) r WHERE NOT EXISTS
 (SELECT 1 FROM jsonb_array_elements(a.mappings) m WHERE m->>'method'=r->>'method' AND m->>'path'=r->>'path'))
 ORDER BY a.approved_at DESC LIMIT 1;
 IF NOT FOUND THEN blockers:=blockers||jsonb_build_array('fresh_authenticated_approval_required');
 ELSE
 IF EXISTS (SELECT 1 FROM request_telemetry rt JOIN deployments td ON td.id=rt.deployment_id
 JOIN jsonb_array_elements(removed) r ON rt.method=r->>'method' AND rt.route IN (r->>'path',(r->>'method')||' '||(r->>'path'))
 WHERE rt.app_id=p.app_id AND rt.account_id=p.account_id AND coalesce(nullif(td.scope,''),'default') IN ('default','prod','production')
 AND rt.received_at>=approval.observation_from AND rt.count>0) THEN
 blockers:=blockers||jsonb_build_array('old_route_observed'); END IF;
 END IF;
 END IF;
 END IF;
 RETURN jsonb_strip_nulls(jsonb_build_object('status',CASE WHEN jsonb_array_length(blockers)>0 THEN 'blocked' WHEN jsonb_array_length(removed)>0 THEN 'passed' ELSE 'not_required' END,
 'removed',removed,'blockers',blockers,'approval_id',coalesce(approval.id::text,''),
 'earliest_approval_at',earliest,'approval_valid_until',approval.valid_until,
 'baseline_contract_sha256',encode(b.doc_sha256,'hex'),'candidate_contract_sha256',encode(c.doc_sha256,'hex')));
END $$;

CREATE OR REPLACE FUNCTION guard_route_removal_traffic() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p app_route_removal_policies%ROWTYPE; result jsonb; fence jsonb;
BEGIN
 IF NEW.status<>'live' OR NEW.traffic_percent<=0 OR
 (TG_OP='UPDATE' AND OLD.status='live' AND NEW.traffic_percent<=OLD.traffic_percent AND NEW.scope IS NOT DISTINCT FROM OLD.scope AND NEW.app_id=OLD.app_id) THEN RETURN NEW; END IF;
 -- Policy setters and deployment activation already take the app lock. This
 -- additionally fences direct SQL/worker/canary transitions with the policy.
 PERFORM 1 FROM apps WHERE id=NEW.app_id FOR UPDATE;
 SELECT * INTO p FROM app_route_removal_policies WHERE app_id=NEW.app_id;
 SELECT value INTO fence FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.route_removal_fences',true),''),'[]')::jsonb)
 WHERE value->>'deployment_id'=NEW.id::text LIMIT 1;
 -- Captures update the policy observation clock after locking their document.
 -- Keep that document-before-policy order while holding the application lock.
 IF fence IS NOT NULL THEN
 PERFORM 1 FROM deployment_openapi_docs WHERE deployment_id IN (NEW.id,p.baseline_deployment_id) ORDER BY deployment_id FOR SHARE;
 PERFORM 1 FROM deployment_openapi_snapshots WHERE deployment_id IN (NEW.id,p.baseline_deployment_id) ORDER BY deployment_id FOR SHARE;
 END IF;
 SELECT * INTO p FROM app_route_removal_policies WHERE app_id=NEW.app_id FOR UPDATE;
 IF coalesce(nullif(NEW.scope,''),'default') NOT IN ('default','prod','production') THEN RETURN NEW; END IF;
 IF NOT FOUND THEN
 IF EXISTS(SELECT 1 FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.route_removal_fences',true),''),'[]')::jsonb) WHERE value->>'deployment_id'=NEW.id::text) THEN
 RAISE EXCEPTION 'route removal policy disappeared after contract preflight' USING ERRCODE='23514',CONSTRAINT='route_removal_required'; END IF;
 RETURN NEW; END IF;
 result:=route_removal_check(NEW.id,coalesce(NEW.scope,''),NEW.app_id);
 SELECT value INTO fence FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.route_removal_fences',true),''),'[]')::jsonb)
 WHERE value->>'deployment_id'=NEW.id::text LIMIT 1;
 IF fence IS NOT NULL AND (p.mode<>'enforce' OR result->>'status'<>'passed' OR
 p.revision::text IS DISTINCT FROM fence->>'policy_revision' OR
 p.baseline_deployment_id::text IS DISTINCT FROM fence->>'baseline_deployment_id' OR
 result->>'approval_id' IS DISTINCT FROM fence->>'approval_id' OR
 result->>'baseline_contract_sha256' IS DISTINCT FROM fence->>'baseline_sha256' OR
 result->>'candidate_contract_sha256' IS DISTINCT FROM fence->>'candidate_sha256') THEN
 RAISE EXCEPTION 'route removal approval changed after contract preflight' USING ERRCODE='23514',CONSTRAINT='route_removal_required';
 END IF;
 IF fence IS NOT NULL AND (
 (coalesce(fence->>'baseline_snapshot_sha256','')<>'' AND NOT EXISTS(SELECT 1 FROM deployment_openapi_snapshots WHERE deployment_id=p.baseline_deployment_id AND sha256=fence->>'baseline_snapshot_sha256')) OR
 (coalesce(fence->>'candidate_snapshot_sha256','')<>'' AND (EXISTS(SELECT 1 FROM deployment_openapi_snapshots WHERE deployment_id=NEW.id AND sha256 IS DISTINCT FROM fence->>'candidate_snapshot_sha256') OR (TG_OP='UPDATE' AND OLD.status='live' AND NOT EXISTS(SELECT 1 FROM deployment_openapi_snapshots WHERE deployment_id=NEW.id AND sha256=fence->>'candidate_snapshot_sha256'))))) THEN
 RAISE EXCEPTION 'route removal contract snapshot changed after preflight' USING ERRCODE='23514',CONSTRAINT='route_removal_required'; END IF;
 IF p.mode='enforce' AND result->>'status'='blocked' THEN
 RAISE EXCEPTION 'route removal blocked' USING ERRCODE='23514',CONSTRAINT='route_removal_required',DETAIL=result::text;
 END IF;
 -- Keep a durable baseline even if callers zero/supersede its row first.
 -- Partial canaries keep the original baseline until a full cutover.
 IF NEW.traffic_percent=100 AND p.baseline_deployment_id IS DISTINCT FROM NEW.id THEN
 UPDATE app_route_removal_policies SET baseline_deployment_id=NEW.id,baseline_since=clock_timestamp() WHERE app_id=NEW.app_id;
 END IF;
 RETURN NEW;
END $$;
DROP FUNCTION IF EXISTS route_removal_coverage_blockers(timestamptz,timestamptz);
DROP FUNCTION IF EXISTS lock_route_removal_coverage();
DROP FUNCTION IF EXISTS record_telemetry_coverage(text,uuid,bigint,boolean,integer,bigint,integer,timestamptz);
DROP TABLE IF EXISTS request_telemetry_coverage;
-- +goose StatementEnd
