-- +goose Up
ALTER TABLE production_lifecycle_reviews ADD COLUMN scope text;
ALTER TABLE production_lifecycle_reviews ADD COLUMN evidence jsonb;
CREATE INDEX production_lifecycle_reviews_cursor ON production_lifecycle_reviews(app_id,id DESC);
-- +goose StatementBegin
CREATE FUNCTION lifecycle_history_graph_ids(snapshot jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT coalesce(jsonb_agg(id ORDER BY id),'[]'::jsonb) FROM (
 SELECT DISTINCT g->>'id' id FROM jsonb_array_elements(coalesce(snapshot,'[]'::jsonb)) b,
 LATERAL jsonb_array_elements(coalesce(nullif(b#>'{project,graphs}','null'::jsonb),'[]'::jsonb)) g
 WHERE g->>'id' IS NOT NULL ORDER BY id LIMIT 64) graphs
$$;
CREATE FUNCTION lifecycle_history_evidence(app uuid, deployment uuid, decision jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 WITH candidates AS (SELECT r.*,coalesce(decision->'lifecycle_approval_ids','[]'::jsonb) ? r.id::text used
 FROM route_lifecycle_approvals r WHERE r.app_id=app AND r.candidate_deployment_id=deployment),
 chosen AS (SELECT * FROM candidates ORDER BY used DESC,approved_at DESC,id DESC LIMIT 20),
 captures AS (
 SELECT d.deployment_id::text id,encode(d.doc_sha256,'hex') sha FROM deployment_openapi_docs d JOIN deployments dep ON dep.id=d.deployment_id
 WHERE d.app_id=app AND (dep.id=deployment OR (dep.status='live' AND dep.traffic_percent>0 AND coalesce(nullif(dep.scope,''),'default') IN ('default','prod','production')))
 UNION SELECT baseline_deployment_id::text,receipt->>'baseline_contract_sha256' FROM chosen
 UNION SELECT m->>'successor_deployment_id',m->>'successor_contract_sha256' FROM chosen,LATERAL jsonb_array_elements(receipt->'mappings') m WHERE nullif(m->>'successor_deployment_id','') IS NOT NULL)
 SELECT jsonb_build_object(
 'truncated',(SELECT count(*)>20 FROM candidates) OR (SELECT count(*)>64 FROM captures) OR EXISTS(SELECT 1 FROM chosen WHERE jsonb_array_length(coalesce(successor_snapshot,'[]'::jsonb))>64),
 'captures',coalesce((SELECT jsonb_agg(jsonb_build_object('deployment_id',id,'sha256',sha) ORDER BY id,sha) FROM (SELECT * FROM captures ORDER BY id,sha LIMIT 64) c),'[]'::jsonb),
 'graph_ids',coalesce((SELECT jsonb_agg(rs.id::text ORDER BY rs.id) FROM project_release_sets rs JOIN apps a ON a.project_id=rs.project_id AND a.account_id=rs.account_id WHERE a.id=app AND rs.active AND rs.environment_slug='production'),'[]'::jsonb),
 'approvals',coalesce((SELECT jsonb_agg(jsonb_build_object('id',id,'used',used,'baseline_deployment_id',baseline_deployment_id,'candidate_deployment_id',candidate_deployment_id,'baseline_contract_sha256',receipt->>'baseline_contract_sha256','candidate_contract_sha256',receipt->>'candidate_contract_sha256','configuration_sha256',receipt->>'configuration_sha256','valid_until',valid_until,'graph_ids',lifecycle_history_graph_ids(successor_snapshot)) ORDER BY used DESC,approved_at DESC,id DESC) FROM chosen),'[]'::jsonb))
$$;
CREATE OR REPLACE FUNCTION guard_production_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gate canary_route_gates%ROWTYPE; fence jsonb;
BEGIN
 IF NEW.status<>'live' OR NEW.traffic_percent<=0 OR coalesce(nullif(NEW.scope,''),'default') NOT IN ('default','prod','production') OR
 (TG_OP='UPDATE' AND OLD.status='live' AND NEW.traffic_percent<=OLD.traffic_percent AND NEW.app_id=OLD.app_id AND NEW.scope IS NOT DISTINCT FROM OLD.scope) THEN RETURN NEW; END IF;
 PERFORM 1 FROM apps WHERE id=NEW.app_id FOR UPDATE;
 SELECT * INTO gate FROM canary_route_gates WHERE app_id=NEW.app_id;

 SELECT value INTO fence FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.lifecycle_fences',true),''),'[]')::jsonb)
 WHERE value->>'deployment_id'=NEW.id::text LIMIT 1;
 IF coalesce(gate.mode,'report')='enforce' AND (fence IS NULL OR fence->>'scope' IS DISTINCT FROM coalesce(nullif(NEW.scope,''),'default') OR fence->'inputs' IS DISTINCT FROM lifecycle_traffic_inputs(NEW.app_id)) THEN
 RAISE EXCEPTION 'production lifecycle review required' USING ERRCODE='23514',CONSTRAINT='production_lifecycle_required'; END IF;
 -- Expiry and capture invalidation are rechecked at the actual traffic write.
 PERFORM 1 FROM route_lifecycle_approvals r WHERE r.id::text IN (SELECT jsonb_array_elements_text(coalesce(nullif(fence->'approval_ids','null'::jsonb),'[]'::jsonb))) ORDER BY r.id FOR SHARE;
 IF coalesce(gate.mode,'report')='enforce' AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(coalesce(nullif(fence->'approval_ids','null'::jsonb),'[]'::jsonb)) x(id)
 WHERE NOT EXISTS(SELECT 1 FROM route_lifecycle_approvals r WHERE r.id::text=x.id AND r.app_id=NEW.app_id
 AND r.invalidated_at IS NULL AND r.valid_until>clock_timestamp()
 AND r.successor_snapshot=lifecycle_approval_successor_bindings(r.app_id,r.receipt))) THEN
 RAISE EXCEPTION 'production lifecycle receipt expired or invalidated' USING ERRCODE='23514',CONSTRAINT='production_lifecycle_required'; END IF;
 INSERT INTO production_lifecycle_reviews(app_id,deployment_id,decision,recovery,scope,evidence) VALUES(NEW.app_id,NEW.id,coalesce(fence->'decision',jsonb_build_object('mode','report','status','report_only','reasons',jsonb_build_array('lifecycle_transaction_review_unavailable'))),coalesce((fence->>'recovery')::boolean,false),coalesce(nullif(NEW.scope,''),'default'),lifecycle_history_evidence(NEW.app_id,NEW.id,coalesce(fence->'decision','{}'::jsonb)));
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_production_lifecycle() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE gate canary_route_gates%ROWTYPE; fence jsonb;
BEGIN
 IF NEW.status<>'live' OR NEW.traffic_percent<=0 OR coalesce(nullif(NEW.scope,''),'default') NOT IN ('default','prod','production') OR
 (TG_OP='UPDATE' AND OLD.status='live' AND NEW.traffic_percent<=OLD.traffic_percent AND NEW.app_id=OLD.app_id AND NEW.scope IS NOT DISTINCT FROM OLD.scope) THEN RETURN NEW; END IF;
 PERFORM 1 FROM apps WHERE id=NEW.app_id FOR UPDATE;
 SELECT * INTO gate FROM canary_route_gates WHERE app_id=NEW.app_id;

 SELECT value INTO fence FROM jsonb_array_elements(coalesce(nullif(current_setting('faas.lifecycle_fences',true),''),'[]')::jsonb)
 WHERE value->>'deployment_id'=NEW.id::text LIMIT 1;
 IF coalesce(gate.mode,'report')='enforce' AND (fence IS NULL OR fence->>'scope' IS DISTINCT FROM coalesce(nullif(NEW.scope,''),'default') OR fence->'inputs' IS DISTINCT FROM lifecycle_traffic_inputs(NEW.app_id)) THEN
 RAISE EXCEPTION 'production lifecycle review required' USING ERRCODE='23514',CONSTRAINT='production_lifecycle_required'; END IF;
 -- Expiry and capture invalidation are rechecked at the actual traffic write.
 PERFORM 1 FROM route_lifecycle_approvals r WHERE r.id::text IN (SELECT jsonb_array_elements_text(coalesce(nullif(fence->'approval_ids','null'::jsonb),'[]'::jsonb))) ORDER BY r.id FOR SHARE;
 IF coalesce(gate.mode,'report')='enforce' AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(coalesce(nullif(fence->'approval_ids','null'::jsonb),'[]'::jsonb)) x(id)
 WHERE NOT EXISTS(SELECT 1 FROM route_lifecycle_approvals r WHERE r.id::text=x.id AND r.app_id=NEW.app_id
 AND r.invalidated_at IS NULL AND r.valid_until>clock_timestamp()
 AND r.successor_snapshot=lifecycle_approval_successor_bindings(r.app_id,r.receipt))) THEN
 RAISE EXCEPTION 'production lifecycle receipt expired or invalidated' USING ERRCODE='23514',CONSTRAINT='production_lifecycle_required'; END IF;
 INSERT INTO production_lifecycle_reviews(app_id,deployment_id,decision,recovery) VALUES(NEW.app_id,NEW.id,coalesce(fence->'decision',jsonb_build_object('mode','report','status','report_only','reasons',jsonb_build_array('lifecycle_transaction_review_unavailable'))),coalesce((fence->>'recovery')::boolean,false));
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP FUNCTION lifecycle_history_evidence(uuid,uuid,jsonb);
DROP FUNCTION lifecycle_history_graph_ids(jsonb);
DROP INDEX production_lifecycle_reviews_cursor;
ALTER TABLE production_lifecycle_reviews DROP COLUMN evidence;
ALTER TABLE production_lifecycle_reviews DROP COLUMN scope;
