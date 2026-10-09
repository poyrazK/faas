-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lifecycle_configuration(app uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('app',jsonb_build_object('id',a.id,'account',a.account_id,'slug',a.slug,
 'consumer_auth_mode',a.consumer_auth_mode,'maintenance_mode',a.maintenance_mode,
 'manifest',a.manifest,'ram_mb',a.ram_mb,'max_concurrency',a.max_concurrency,'idle_timeout_s',a.idle_timeout_s,'type',a.type,'cpu_millicores',a.cpu_millicores,'scaling_policy',a.scaling_policy,'request_rate_limit_rps',a.request_rate_limit_rps,'request_rate_limit_burst',a.request_rate_limit_burst),
 'account',jsonb_build_object('plan',c.plan,'status',c.status,'past_due_at',c.past_due_at,'abuse_hold_at',c.abuse_hold_at),
 'rules',coalesce((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id) FROM edge_rules r WHERE r.app_id=a.id),'[]'::jsonb))
 FROM apps a JOIN accounts c ON c.id=a.account_id WHERE a.id=app
$$;
ALTER TABLE route_lifecycle_approvals ADD COLUMN IF NOT EXISTS configuration_snapshot jsonb;
-- Old receipts lack the additional binding and must be reviewed again.
UPDATE route_lifecycle_approvals SET invalidated_at=clock_timestamp() WHERE invalidated_at IS NULL;
CREATE OR REPLACE FUNCTION lifecycle_traffic_inputs(app uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('configuration',lifecycle_configuration(app),
 'gate',coalesce((SELECT to_jsonb(g) FROM canary_route_gates g WHERE g.app_id=app),'{}'::jsonb),
 'requirements',coalesce((SELECT to_jsonb(s) FROM saved_route_requirements s WHERE s.app_id=app),'{}'::jsonb),
 'removal',coalesce((SELECT to_jsonb(p)-'baseline_since'-'baseline_deployment_id' FROM app_route_removal_policies p WHERE p.app_id=app),'{}'::jsonb),
 'captures',coalesce((SELECT jsonb_agg(jsonb_build_object('id',d.deployment_id,'sha',encode(d.doc_sha256,'hex'),'document_sha',encode(sha256(convert_to(d.doc::text,'UTF8')),'hex'),'truncated',d.truncated,'captured_at',d.captured_at,'updated_at',d.updated_at) ORDER BY d.deployment_id) FROM deployment_openapi_docs d WHERE d.app_id=app),'[]'::jsonb))
$$;
CREATE TABLE IF NOT EXISTS production_lifecycle_reviews (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 reviewed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 decision jsonb NOT NULL CHECK(jsonb_typeof(decision)='object'),
 recovery boolean NOT NULL DEFAULT false
);
CREATE INDEX IF NOT EXISTS production_lifecycle_reviews_app ON production_lifecycle_reviews(app_id,reviewed_at DESC);
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
DROP TRIGGER IF EXISTS deployment_production_lifecycle_guard ON deployments;
CREATE TRIGGER deployment_production_lifecycle_guard BEFORE UPDATE OF status,traffic_percent,scope,app_id ON deployments FOR EACH ROW EXECUTE FUNCTION guard_production_lifecycle();
DROP TRIGGER IF EXISTS deployment_production_lifecycle_insert_guard ON deployments;
CREATE TRIGGER deployment_production_lifecycle_insert_guard AFTER INSERT ON deployments FOR EACH ROW EXECUTE FUNCTION guard_production_lifecycle();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER deployment_production_lifecycle_insert_guard ON deployments;
DROP TRIGGER deployment_production_lifecycle_guard ON deployments;
DROP FUNCTION guard_production_lifecycle();
DROP TABLE production_lifecycle_reviews;
DROP FUNCTION lifecycle_traffic_inputs(uuid);
ALTER TABLE route_lifecycle_approvals DROP COLUMN configuration_snapshot;
DROP FUNCTION lifecycle_configuration(uuid);
