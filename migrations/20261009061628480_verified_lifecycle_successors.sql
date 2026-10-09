-- +goose Up
ALTER TABLE route_lifecycle_approvals ADD COLUMN IF NOT EXISTS successor_snapshot jsonb;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lifecycle_successor_bindings(source uuid, mappings jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT coalesce(jsonb_agg(jsonb_build_object('mapping',m,'app',a.id,'status',a.status,'visibility',a.visibility,'org_id',a.org_id,'project_id',a.project_id,'only_declared_routes',a.only_declared_routes,'declared_routes',a.declared_routes,
 'configuration',lifecycle_configuration(a.id),
 'domain', (SELECT jsonb_build_object('domain',d.domain,'app_id',d.app_id,'environment_id',d.environment_id,'verified_at',d.verified_at) FROM custom_domains d WHERE d.domain::text=split_part(split_part(m->>'successor_url','/',3),':',1)),
 'claimed',EXISTS(SELECT 1 FROM tenant_hostnames h WHERE h.hostname::text=split_part(split_part(m->>'successor_url','/',3),':',1)),
 'capture',(SELECT jsonb_build_object('sha',encode(c.doc_sha256,'hex'),'document_sha',encode(sha256(convert_to(c.doc::text,'UTF8')),'hex'),'truncated',c.truncated,'captured_at',c.captured_at,'updated_at',c.updated_at) FROM deployment_openapi_docs c WHERE c.deployment_id=coalesce(nullif(m->>'successor_deployment_id','')::uuid, nullif(m->>'candidate_deployment_id','')::uuid) AND c.app_id=a.id),
 'routes',CASE WHEN a.id=source THEN '[]'::jsonb ELSE coalesce((SELECT jsonb_agg(jsonb_build_object('id',d.id,'scope',d.scope,'status',d.status,'traffic',d.traffic_percent) ORDER BY d.id) FROM deployments d WHERE d.app_id=a.id AND d.status='live' AND d.traffic_percent>0 AND coalesce(nullif(d.scope,''),'default') IN ('default','prod','production')),'[]'::jsonb) END) ORDER BY m->>'method',m->>'path'),'[]'::jsonb)
 FROM jsonb_array_elements(mappings) m LEFT JOIN apps a ON a.id=coalesce(nullif(m->>'successor_app_id','')::uuid,source)
$$;
CREATE OR REPLACE FUNCTION lifecycle_approval_successor_bindings(source uuid, receipt jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT lifecycle_successor_bindings(source,coalesce((SELECT jsonb_agg(m || jsonb_build_object('candidate_deployment_id',receipt->>'candidate_deployment_id')) FROM jsonb_array_elements(receipt->'mappings') m),'[]'::jsonb))
$$;
CREATE OR REPLACE FUNCTION invalidate_lifecycle_successor_routes() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE oldrow jsonb; newrow jsonb; affected uuid; owner uuid; hostname text;
BEGIN
 IF TG_OP<>'INSERT' THEN oldrow:=to_jsonb(OLD); END IF;
 IF TG_OP<>'DELETE' THEN newrow:=to_jsonb(NEW); END IF;
 IF TG_TABLE_NAME='tenant_hostnames' THEN
  UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp() WHERE r.invalidated_at IS NULL AND EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE split_part(m->>'successor_url','/',3) IN (oldrow->>'hostname',newrow->>'hostname'));
  RETURN NULL;
 END IF;
 FOR affected IN SELECT DISTINCT id FROM (VALUES(coalesce(oldrow->>'app_id',CASE WHEN TG_TABLE_NAME='apps' THEN oldrow->>'id' END)::uuid),(coalesce(newrow->>'app_id',CASE WHEN TG_TABLE_NAME='apps' THEN newrow->>'id' END)::uuid)) ids(id) WHERE id IS NOT NULL LOOP
  SELECT account_id INTO owner FROM apps WHERE id=affected;
  PERFORM 1 FROM accounts WHERE id=owner FOR UPDATE;
  UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp() WHERE r.invalidated_at IS NULL AND
   ((TG_TABLE_NAME<>'deployments' AND r.app_id=affected) OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE m->>'successor_app_id'=affected::text AND (TG_TABLE_NAME<>'deployments' OR affected<>r.app_id)));
 END LOOP;
 RETURN NULL;
END $$;
DROP TRIGGER IF EXISTS lifecycle_successor_domains ON custom_domains;
CREATE TRIGGER lifecycle_successor_domains AFTER INSERT OR DELETE OR UPDATE OF app_id,domain,environment_id,verified_at ON custom_domains FOR EACH ROW EXECUTE FUNCTION invalidate_lifecycle_successor_routes();
DROP TRIGGER IF EXISTS lifecycle_successor_apps ON apps;
CREATE TRIGGER lifecycle_successor_apps AFTER DELETE OR UPDATE OF status,slug,manifest,visibility,org_id,project_id,account_id,only_declared_routes,declared_routes,consumer_auth_mode,maintenance_mode ON apps FOR EACH ROW EXECUTE FUNCTION invalidate_lifecycle_successor_routes();
DROP TRIGGER IF EXISTS lifecycle_successor_rules ON edge_rules;
CREATE TRIGGER lifecycle_successor_rules AFTER INSERT OR UPDATE OR DELETE ON edge_rules FOR EACH ROW EXECUTE FUNCTION invalidate_lifecycle_successor_routes();
DROP TRIGGER IF EXISTS lifecycle_successor_deployments ON deployments;
CREATE TRIGGER lifecycle_successor_deployments AFTER INSERT OR DELETE OR UPDATE OF app_id,status,traffic_percent,scope ON deployments FOR EACH ROW EXECUTE FUNCTION invalidate_lifecycle_successor_routes();
DROP TRIGGER IF EXISTS lifecycle_successor_tenant_hosts ON tenant_hostnames;
CREATE TRIGGER lifecycle_successor_tenant_hosts AFTER INSERT OR DELETE OR UPDATE OF hostname,surface_id,verified_at ON tenant_hostnames FOR EACH ROW EXECUTE FUNCTION invalidate_lifecycle_successor_routes();
CREATE OR REPLACE FUNCTION invalidate_route_lifecycle_approvals() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp()
 WHERE r.invalidated_at IS NULL AND ((r.app_id=OLD.app_id AND (r.baseline_deployment_id=OLD.deployment_id OR r.candidate_deployment_id=OLD.deployment_id)) OR EXISTS(SELECT 1 FROM jsonb_array_elements(r.receipt->'mappings') m WHERE m->>'successor_deployment_id'=OLD.deployment_id::text));
 RETURN NULL;
END $$;
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
UPDATE route_lifecycle_approvals SET invalidated_at=clock_timestamp() WHERE invalidated_at IS NULL;

-- +goose Down
DROP TRIGGER lifecycle_successor_domains ON custom_domains;
DROP TRIGGER lifecycle_successor_apps ON apps;
DROP TRIGGER lifecycle_successor_rules ON edge_rules;
DROP TRIGGER lifecycle_successor_deployments ON deployments;
DROP TRIGGER lifecycle_successor_tenant_hosts ON tenant_hostnames;
DROP FUNCTION invalidate_lifecycle_successor_routes();
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
 IF coalesce(gate.mode,'report')='enforce' AND EXISTS(SELECT 1 FROM jsonb_array_elements_text(coalesce(nullif(fence->'approval_ids','null'::jsonb),'[]'::jsonb)) x(id)
 WHERE NOT EXISTS(SELECT 1 FROM route_lifecycle_approvals r WHERE r.id::text=x.id AND r.app_id=NEW.app_id
 AND r.invalidated_at IS NULL AND r.valid_until>clock_timestamp())) THEN
 RAISE EXCEPTION 'production lifecycle receipt expired or invalidated' USING ERRCODE='23514',CONSTRAINT='production_lifecycle_required'; END IF;
 INSERT INTO production_lifecycle_reviews(app_id,deployment_id,decision,recovery) VALUES(NEW.app_id,NEW.id,coalesce(fence->'decision',jsonb_build_object('mode','report','status','report_only','reasons',jsonb_build_array('lifecycle_transaction_review_unavailable'))),coalesce((fence->>'recovery')::boolean,false));
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invalidate_route_lifecycle_approvals() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 UPDATE route_lifecycle_approvals SET invalidated_at=clock_timestamp()
 WHERE app_id=OLD.app_id AND invalidated_at IS NULL
 AND (baseline_deployment_id=OLD.deployment_id OR candidate_deployment_id=OLD.deployment_id);
 RETURN NULL;
END;
$$;
-- +goose StatementEnd
DROP FUNCTION lifecycle_approval_successor_bindings(uuid,jsonb);
DROP FUNCTION lifecycle_successor_bindings(uuid,jsonb);
ALTER TABLE route_lifecycle_approvals DROP COLUMN successor_snapshot;
