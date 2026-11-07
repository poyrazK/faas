-- ADR-20261028110044 amendment 6: route removal evidence counts the removed
-- route's traffic under every label the gateway can give it.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- A removed operation's telemetry pattern: the method (GET also answers HEAD)
-- and the path with each {parameter} as one segment, trailing slash optional.
-- It matches the declared template label, the gateway's inferred {id} label
-- and raw undeclared paths alike (ADR-20261028110044 amendment 6).
CREATE OR REPLACE FUNCTION route_removal_route_pattern(method text, path text) RETURNS text LANGUAGE sql IMMUTABLE AS $f$
 SELECT '^((' || CASE WHEN upper(method)='GET' THEN 'GET|HEAD' ELSE upper(method) END || ') )?'
  || regexp_replace(
       regexp_replace(CASE WHEN path='/' THEN '/' ELSE rtrim(path,'/') END, '([][.^$*+?(){}|\\])', '\\\1', 'g'),
       '\\\{[^/}]*\\\}', '[^/]+', 'g')
  || CASE WHEN path='/' THEN '' ELSE '/?' END || '$'
$f$;

-- Whether production traffic since `since` reached any removed operation,
-- whatever label the gateway gave it. Labels of operations the candidate
-- keeps (exact templates, HEAD counted with GET) are not the removed route.
CREATE OR REPLACE FUNCTION route_removal_route_observed(target_app uuid, target_account uuid, removed jsonb, retained jsonb, since timestamptz) RETURNS boolean LANGUAGE sql STABLE AS $f$
 WITH r AS (
  SELECT upper(x->>'method') AS method, route_removal_route_pattern(x->>'method', x->>'path') AS pattern
  FROM jsonb_array_elements(coalesce(removed,'[]'::jsonb)) x
  WHERE coalesce(x->>'method','')<>'' AND coalesce(x->>'path','') LIKE '/%'
 ), k AS (
  SELECT o.method, CASE WHEN o.path='/' THEN '/' ELSE rtrim(o.path,'/') END AS path
  FROM route_removal_operations(coalesce(retained,'{}'::jsonb)) o
 )
 SELECT EXISTS (
  SELECT 1 FROM request_telemetry rt JOIN deployments td ON td.id=rt.deployment_id
  JOIN r ON (rt.method=r.method OR (r.method='GET' AND rt.method='HEAD')) AND rt.route ~ r.pattern
  WHERE rt.app_id=target_app AND rt.account_id=target_account
   AND coalesce(nullif(td.scope,''),'default') IN ('default','prod','production')
   AND rt.received_at>=since AND rt.count>0
   AND NOT EXISTS (SELECT 1 FROM k WHERE (k.method=rt.method OR (k.method='GET' AND rt.method='HEAD'))
    AND rt.route IN (k.path, rt.method||' '||k.path))
 )
$f$;

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
 blockers:=blockers||route_removal_coverage_blockers(p.app_id,date_trunc('minute',stamp-interval '2 minutes')-make_interval(secs=>p.grace_seconds),date_trunc('minute',stamp-interval '2 minutes'));
 SELECT greatest(earliest,date_trunc('minute',max(healthy_since))+make_interval(secs=>p.grace_seconds)+interval '3 minutes') INTO earliest FROM request_telemetry_coverage cv JOIN compute_nodes n ON n.name=cv.node_name WHERE n.active;
 SELECT greatest(earliest,date_trunc('minute',max(g.last_gap_at))+make_interval(secs=>p.grace_seconds)+interval '3 minutes') INTO earliest FROM request_telemetry_app_gaps g JOIN compute_nodes n ON n.name=g.node_name WHERE n.active AND g.app_id=p.app_id;
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
 blockers:=blockers||route_removal_coverage_blockers(p.app_id,approval.observation_from,date_trunc('minute',stamp-interval '2 minutes'));
 IF route_removal_route_observed(p.app_id,p.account_id,removed,c.doc,approval.observation_from) THEN
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
 earliest:=date_trunc('minute',greatest(p.baseline_since,b.captured_at))+make_interval(secs=>p.grace_seconds)+interval '3 minutes';
 blockers:=blockers||route_removal_coverage_blockers(p.app_id,date_trunc('minute',stamp-interval '2 minutes')-make_interval(secs=>p.grace_seconds),date_trunc('minute',stamp-interval '2 minutes'));
 SELECT greatest(earliest,date_trunc('minute',max(healthy_since))+make_interval(secs=>p.grace_seconds)+interval '3 minutes') INTO earliest FROM request_telemetry_coverage cv JOIN compute_nodes n ON n.name=cv.node_name WHERE n.active;
 SELECT greatest(earliest,date_trunc('minute',max(g.last_gap_at))+make_interval(secs=>p.grace_seconds)+interval '3 minutes') INTO earliest FROM request_telemetry_app_gaps g JOIN compute_nodes n ON n.name=g.node_name WHERE n.active AND g.app_id=p.app_id;
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
 blockers:=blockers||route_removal_coverage_blockers(p.app_id,approval.observation_from,date_trunc('minute',stamp-interval '2 minutes'));
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
DROP FUNCTION IF EXISTS route_removal_route_observed(uuid, uuid, jsonb, jsonb, timestamptz);
DROP FUNCTION IF EXISTS route_removal_route_pattern(text, text);
-- +goose StatementEnd
