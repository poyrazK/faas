-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_route_removal_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 mode text NOT NULL CHECK (mode IN ('report','enforce')),
 revision integer NOT NULL CHECK (revision>0),
 grace_seconds bigint NOT NULL CHECK (grace_seconds BETWEEN 3600 AND 7776000),
 max_approval_age_seconds bigint NOT NULL CHECK (max_approval_age_seconds BETWEEN 60 AND 259200),
 baseline_deployment_id uuid REFERENCES deployments(id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED,
 baseline_since timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS route_removal_approvals (
 id uuid PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 policy_revision integer NOT NULL,
 baseline_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 candidate_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 baseline_sha256 bytea NOT NULL CHECK (octet_length(baseline_sha256)=32),
 candidate_sha256 bytea NOT NULL CHECK (octet_length(candidate_sha256)=32),
 mapping_sha256 text NOT NULL CHECK (mapping_sha256 ~ '^[0-9a-f]{64}$'),
 mappings jsonb NOT NULL CHECK (jsonb_typeof(mappings)='array' AND jsonb_array_length(mappings) BETWEEN 1 AND 2000),
 approved_by text NOT NULL,
 approved_at timestamptz NOT NULL,
 valid_until timestamptz NOT NULL,
 observation_from timestamptz NOT NULL,
 observation_until timestamptz NOT NULL,
 receipt jsonb NOT NULL,
 CHECK (observation_from < observation_until AND observation_until <= approved_at AND approved_at < valid_until)
);
CREATE INDEX IF NOT EXISTS route_removal_approval_lookup ON route_removal_approvals(app_id,candidate_deployment_id,valid_until);
CREATE TABLE IF NOT EXISTS route_removal_policy_history (
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 revision integer NOT NULL,
 changed_by text NOT NULL,
 changed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 policy jsonb NOT NULL,
 PRIMARY KEY(app_id,revision)
);

CREATE OR REPLACE FUNCTION reset_route_removal_observation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR TG_OP='INSERT' OR NEW.doc_sha256 IS DISTINCT FROM OLD.doc_sha256 OR NEW.truncated IS DISTINCT FROM OLD.truncated THEN
 UPDATE app_route_removal_policies SET baseline_since=clock_timestamp()
 WHERE baseline_deployment_id=CASE WHEN TG_OP='DELETE' THEN OLD.deployment_id ELSE NEW.deployment_id END;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS route_removal_capture_observation ON deployment_openapi_docs;
CREATE TRIGGER route_removal_capture_observation AFTER INSERT OR UPDATE OR DELETE ON deployment_openapi_docs
FOR EACH ROW EXECUTE FUNCTION reset_route_removal_observation();

CREATE OR REPLACE FUNCTION route_removal_operations(document jsonb) RETURNS TABLE(method text,path text)
LANGUAGE sql IMMUTABLE AS $$
 SELECT upper(o.key),p.key FROM jsonb_each(document->'paths') p
 CROSS JOIN LATERAL jsonb_each(p.value) o
 WHERE lower(o.key) IN ('get','put','post','delete','options','head','patch','trace')
$$;

-- This shared evaluator is also called from the API. It uses authoritative
-- captures, stored approvals and unbounded aggregate traffic, never a CLI flag.
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
DROP TRIGGER IF EXISTS deployment_route_removal_guard ON deployments;
CREATE TRIGGER deployment_route_removal_guard BEFORE UPDATE OF status,traffic_percent,scope,app_id ON deployments
FOR EACH ROW EXECUTE FUNCTION guard_route_removal_traffic();
DROP TRIGGER IF EXISTS deployment_route_removal_insert_guard ON deployments;
CREATE TRIGGER deployment_route_removal_insert_guard AFTER INSERT ON deployments
FOR EACH ROW EXECUTE FUNCTION guard_route_removal_traffic();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS deployment_route_removal_guard ON deployments;
DROP TRIGGER IF EXISTS deployment_route_removal_insert_guard ON deployments;
DROP FUNCTION IF EXISTS guard_route_removal_traffic();
DROP FUNCTION IF EXISTS route_removal_check(uuid,text,uuid);
DROP FUNCTION IF EXISTS route_removal_operations(jsonb);
DROP TRIGGER IF EXISTS route_removal_capture_observation ON deployment_openapi_docs;
DROP FUNCTION IF EXISTS reset_route_removal_observation();
DROP TABLE IF EXISTS route_removal_policy_history;
DROP TABLE IF EXISTS route_removal_approvals;
DROP TABLE IF EXISTS app_route_removal_policies;
-- +goose StatementEnd
