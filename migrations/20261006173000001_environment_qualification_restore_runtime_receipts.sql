-- ADR-568: restored runtime delivery belongs to its distinct native target.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_qualification_runtime_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE q environment_workload_qualification_requests%ROWTYPE; i instances%ROWTYPE;
        b environment_qualification_restore_reservations%ROWTYPE; restoring boolean:=false;
BEGIN
 SELECT * INTO i FROM instances WHERE id=NEW.instance_id;
	IF i.id IS NULL OR NOT EXISTS(SELECT 1 FROM deployments WHERE id=i.deployment_id AND environment_workload_runtime IS NOT NULL) THEN RETURN NEW; END IF;
 SELECT * INTO q FROM environment_workload_qualification_requests WHERE reserved_instance_id=i.id;
 IF q.id IS NULL THEN
  SELECT * INTO b FROM environment_qualification_restore_reservations WHERE instance_id=i.id;
  IF b.instance_id IS NOT NULL THEN
   SELECT * INTO q FROM environment_workload_qualification_requests
    WHERE id=b.request_id AND reserved_instance_id=b.capture_instance_id AND attempt=b.attempt;
   restoring:=q.id IS NOT NULL;
  END IF;
 END IF;
 IF q.id IS NOT NULL THEN
  PERFORM s.id FROM environment_git_sources s JOIN environment_workload_graphs g ON g.source_id=s.id WHERE g.id=q.graph_id FOR UPDATE OF s;
  PERFORM a.id FROM apps a JOIN environment_workload_graphs g ON g.id=q.graph_id
   JOIN LATERAL jsonb_array_elements(g.members) m ON a.id=(m->>'app_id')::uuid ORDER BY a.id FOR UPDATE OF a;
  SELECT * INTO q FROM environment_workload_qualification_requests WHERE id=q.id FOR UPDATE;
  PERFORM n.id FROM compute_nodes n WHERE n.id=i.node_id FOR SHARE OF n;
  PERFORM c.id FROM accounts c JOIN apps a ON a.account_id=c.id WHERE a.id=i.app_id FOR SHARE OF c;
  SELECT * INTO i FROM instances WHERE id=NEW.instance_id FOR UPDATE;
 END IF;
 IF restoring AND NOT EXISTS(SELECT 1 FROM environment_qualification_executions e
	   WHERE e.instance_id=i.id AND e.request_id=q.id AND e.capture_instance_id=b.capture_instance_id
	    AND e.frame=(environment_qualification_execution_frame(q,i)||jsonb_build_object('capture_instance_id',b.capture_instance_id))
    AND e.dispatch_started AND e.retired_at IS NULL AND environment_qualification_restore_current(q.id,b.capture_instance_id)) THEN
  RAISE EXCEPTION 'restored runtime receipt requires its live dispatched target' USING ERRCODE='23514';
 END IF;
 IF q.id IS NULL OR q.phase<>'claimed' OR q.lease_until<=clock_timestamp()
  OR q.lease_token IS DISTINCT FROM current_setting('gregale.gitops_qualification',true)
  OR q.app_id<>i.app_id OR q.deployment_id<>i.deployment_id OR i.state<>'running'
  OR i.ram_mb IS DISTINCT FROM (SELECT a.ram_mb FROM apps a WHERE a.id=i.app_id)
  OR NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=i.node_id AND n.active AND n.lifecycle='active')
  OR NEW.wake_id IS DISTINCT FROM i.wake_id OR NEW.scope IS DISTINCT FROM q.frozen_inputs->>'scope'
  OR NOT environment_workload_qualification_inputs_current(q.id)
  OR NOT environment_runtime_inputs_fresh(i.app_id,NEW.scope,NEW.boundary_at,NEW.variables,NEW.secret_versions,NEW.all_secrets,NEW.secret_refs,NEW.sidecar_secret_versions) THEN
  RAISE EXCEPTION 'environment runtime evidence requires its current qualification attempt and delivered inputs' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_qualification_runtime_receipt ON instance_runtime_config_receipts;
CREATE TRIGGER guard_environment_qualification_runtime_receipt BEFORE INSERT OR UPDATE ON instance_runtime_config_receipts
 FOR EACH ROW EXECUTE FUNCTION guard_environment_qualification_runtime_receipt();
-- +goose Down
-- Keep restored runtime evidence fenced across binary rollback.
SELECT 1;
