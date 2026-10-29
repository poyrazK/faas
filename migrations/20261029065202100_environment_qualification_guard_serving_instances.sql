-- migration-compat: expand
-- filename: 20261029065202100_environment_qualification_guard_serving_instances.sql
--
-- ADR 20261023093000 (acceptance record): the legacy-qualification fallback of
-- guard_environment_qualification_physical_retirement refused every terminal
-- state of a frameless instance on a GitOps workload deployment. Activation
-- unholds that deployment and serves it, keeping environment_workload_runtime,
-- so its ordinary serving instances could never fail, park or be removed: a
-- serving cold boot that timed out stayed cold_booting forever (n2,
-- 2026-10-29). A frameless legacy qualification can only sit on a held
-- candidate; only that fallback changes.
--
-- guard_environment_qualification_runtime_receipt treated the same serving
-- instances as qualification attempts and refused their runtime receipts, so
-- no activated GitOps workload could finish a serving wake. Like the instance
-- reservation guard, it now lets a frameless instance of an unheld live
-- deployment through.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION guard_environment_qualification_physical_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e environment_qualification_executions%ROWTYPE;
BEGIN
 SELECT * INTO e FROM environment_qualification_executions WHERE instance_id=OLD.id;
 IF e.instance_id IS NOT NULL THEN
  IF TG_OP='UPDATE' AND NEW.state='running' AND (NOT e.dispatch_started OR e.retired_at IS NOT NULL) THEN
   RAISE EXCEPTION 'qualification runtime requires a dispatched unretired attempt' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' AND e.retired_at IS NULL OR TG_OP='UPDATE' AND NEW.state NOT IN ('cold_booting','waking','running','draining','warm') AND e.retired_at IS NULL THEN
   RAISE EXCEPTION 'qualification reservation requires attempt-bound physical retirement' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.ram_mb)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.ram_mb) THEN
   RAISE EXCEPTION 'qualification placement survives parent removal' USING ERRCODE='23514';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL AND environment_workload_held)
  AND (TG_OP='DELETE' OR NEW.state NOT IN ('cold_booting','waking','running','draining','warm')) THEN
  RAISE EXCEPTION 'legacy qualification has no recoverable execution frame' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
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
 -- An activated workload's own serving instances deliver like any deployment's;
 -- the qualification attempt checks below apply to candidates only.
 IF q.id IS NULL AND EXISTS(SELECT 1 FROM deployments WHERE id=i.deployment_id AND app_id=i.app_id
  AND NOT environment_workload_held AND status='live') THEN RETURN NEW; END IF;
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

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION guard_environment_qualification_physical_retirement() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE e environment_qualification_executions%ROWTYPE;
BEGIN
 SELECT * INTO e FROM environment_qualification_executions WHERE instance_id=OLD.id;
 IF e.instance_id IS NOT NULL THEN
  IF TG_OP='UPDATE' AND NEW.state='running' AND (NOT e.dispatch_started OR e.retired_at IS NOT NULL) THEN
   RAISE EXCEPTION 'qualification runtime requires a dispatched unretired attempt' USING ERRCODE='23514';
  END IF;
  IF TG_OP='DELETE' AND e.retired_at IS NULL OR TG_OP='UPDATE' AND NEW.state NOT IN ('cold_booting','waking','running','draining','warm') AND e.retired_at IS NULL THEN
   RAISE EXCEPTION 'qualification reservation requires attempt-bound physical retirement' USING ERRCODE='23514';
  END IF;
  IF TG_OP='UPDATE' AND ROW(NEW.id,NEW.app_id,NEW.deployment_id,NEW.node_id,NEW.wake_id,NEW.ram_mb)
   IS DISTINCT FROM ROW(OLD.id,OLD.app_id,OLD.deployment_id,OLD.node_id,OLD.wake_id,OLD.ram_mb) THEN
   RAISE EXCEPTION 'qualification placement survives parent removal' USING ERRCODE='23514';
  END IF;
 ELSIF EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id AND environment_workload_runtime IS NOT NULL)
  AND (TG_OP='DELETE' OR NEW.state NOT IN ('cold_booting','waking','running','draining','warm')) THEN
  RAISE EXCEPTION 'legacy qualification has no recoverable execution frame' USING ERRCODE='23514';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
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
