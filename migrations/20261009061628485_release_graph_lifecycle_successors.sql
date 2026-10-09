-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION lifecycle_project_successor(app uuid, deployment uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('graphs',coalesce((SELECT jsonb_agg(to_jsonb(rs)||jsonb_build_object('members',coalesce((SELECT jsonb_agg(to_jsonb(m) ORDER BY m.app_id) FROM project_release_members m WHERE m.release_id=rs.id),'[]'::jsonb)) ORDER BY rs.id) FROM project_release_sets rs WHERE rs.project_id=a.project_id AND rs.account_id=a.account_id AND rs.environment_slug='production' AND rs.active),'[]'::jsonb),
 'spec',(SELECT to_jsonb(s)||jsonb_build_object('settings_raw',s.settings::text,'environment',to_jsonb(e)) FROM project_environment_workload_deployment_specs p JOIN project_environment_workload_specs s ON s.id=p.spec_id JOIN project_environments e ON e.id=s.environment_id WHERE p.deployment_id=deployment AND s.app_id=a.id AND e.project_id=a.project_id AND e.account_id=a.account_id AND e.slug='production'),
 'live',EXISTS(SELECT 1 FROM deployments d WHERE d.id=deployment AND d.app_id=a.id AND d.status='live' AND d.scope='production'))
 FROM apps a WHERE a.id=app AND a.project_id IS NOT NULL
$$;
ALTER FUNCTION lifecycle_successor_bindings(uuid,jsonb) RENAME TO lifecycle_successor_bindings_v736;
CREATE FUNCTION lifecycle_successor_bindings(source uuid,mappings jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT coalesce(jsonb_agg(item||jsonb_build_object('project',lifecycle_project_successor((item->>'app')::uuid,coalesce(nullif(item#>>'{mapping,successor_deployment_id}','')::uuid,nullif(item#>>'{mapping,candidate_deployment_id}','')::uuid))) ORDER BY item#>>'{mapping,method}',item#>>'{mapping,path}'),'[]'::jsonb)
 FROM jsonb_array_elements(lifecycle_successor_bindings_v736(source,mappings)) item
$$;
-- Rebind the receipt helper to the wrapper after renaming the prior implementation.
CREATE OR REPLACE FUNCTION lifecycle_approval_successor_bindings(source uuid,receipt jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT lifecycle_successor_bindings(source,coalesce((SELECT jsonb_agg(m || jsonb_build_object('candidate_deployment_id',receipt->>'candidate_deployment_id')) FROM jsonb_array_elements(receipt->'mappings') m),'[]'::jsonb))
$$;
CREATE FUNCTION invalidate_lifecycle_project_successors() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 -- A generation change is permanent, even if the old graph is reactivated.
 -- Include retained receipts so neither rollback nor restore revives them.
 UPDATE route_lifecycle_approvals r SET invalidated_at=clock_timestamp()
 WHERE r.invalidated_at IS NULL AND r.successor_snapshot IS DISTINCT FROM lifecycle_approval_successor_bindings(r.app_id,r.receipt);
 RETURN NULL;
END $$;
CREATE TRIGGER lifecycle_successor_release_sets AFTER INSERT OR UPDATE OR DELETE ON project_release_sets FOR EACH STATEMENT EXECUTE FUNCTION invalidate_lifecycle_project_successors();
CREATE TRIGGER lifecycle_successor_release_members AFTER INSERT OR UPDATE OR DELETE ON project_release_members FOR EACH STATEMENT EXECUTE FUNCTION invalidate_lifecycle_project_successors();
CREATE TRIGGER lifecycle_successor_workload_pins AFTER INSERT OR UPDATE OR DELETE ON project_environment_workload_deployment_specs FOR EACH STATEMENT EXECUTE FUNCTION invalidate_lifecycle_project_successors();
CREATE TRIGGER lifecycle_successor_workload_specs AFTER UPDATE OR DELETE ON project_environment_workload_specs FOR EACH STATEMENT EXECUTE FUNCTION invalidate_lifecycle_project_successors();
CREATE TRIGGER lifecycle_successor_environments AFTER UPDATE OR DELETE ON project_environments FOR EACH STATEMENT EXECUTE FUNCTION invalidate_lifecycle_project_successors();
-- +goose StatementEnd
UPDATE route_lifecycle_approvals SET invalidated_at=clock_timestamp() WHERE invalidated_at IS NULL;
-- +goose Down
DROP TRIGGER lifecycle_successor_release_sets ON project_release_sets;
DROP TRIGGER lifecycle_successor_release_members ON project_release_members;
DROP TRIGGER lifecycle_successor_workload_pins ON project_environment_workload_deployment_specs;
DROP TRIGGER lifecycle_successor_workload_specs ON project_environment_workload_specs;
DROP TRIGGER lifecycle_successor_environments ON project_environments;
DROP FUNCTION invalidate_lifecycle_project_successors();
DROP FUNCTION lifecycle_successor_bindings(uuid,jsonb);
ALTER FUNCTION lifecycle_successor_bindings_v736(uuid,jsonb) RENAME TO lifecycle_successor_bindings;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION lifecycle_approval_successor_bindings(source uuid,receipt jsonb) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT lifecycle_successor_bindings(source,coalesce((SELECT jsonb_agg(m || jsonb_build_object('candidate_deployment_id',receipt->>'candidate_deployment_id')) FROM jsonb_array_elements(receipt->'mappings') m),'[]'::jsonb))
$$;
-- +goose StatementEnd
DROP FUNCTION lifecycle_project_successor(uuid,uuid);
