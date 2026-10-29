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
-- +goose StatementEnd
