-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Frozen runtime is retained history. Only explicit source retirement releases
-- ownership; absent authority fails closed, including legacy malformed markers.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION environment_workload_ownership_active(d deployments) RETURNS boolean
LANGUAGE sql STABLE AS $$
 SELECT d.environment_workload_runtime IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM environment_git_sources source
  WHERE source.id::text=d.environment_workload_runtime->>'source_id' AND source.detached
 );
$$;
-- +goose StatementEnd

-- Preserve the existing preparation, artifact, hold and activation guards. A
-- fresh ordinary deployment may replace detached history; frozen candidates
-- themselves still require graph activation and cannot be promoted manually.
-- Accept the corrected predicates on replay without rewriting unrelated fences.
-- +goose StatementBegin
DO $handoff$
DECLARE definition text;
BEGIN
 definition:=pg_get_functiondef('guard_environment_workload_candidate()'::regprocedure);
 IF position('d.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL' IN definition)>0 THEN
  definition:=replace(definition,
   'd.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL',
   'd.scope=NEW.scope AND environment_workload_ownership_active(d)');
 ELSIF position('d.scope=NEW.scope AND environment_workload_ownership_active(d)' IN definition)=0 THEN
  RAISE EXCEPTION 'candidate ownership guard differs from the expected migration contract';
 END IF;
 IF position('environment_workload_retirement_drain_authorized(OLD))' IN definition)>0 THEN
  definition:=replace(definition,
   'environment_workload_retirement_drain_authorized(OLD))',
   '(environment_workload_retirement_drain_authorized(OLD) OR NOT environment_workload_ownership_active(OLD)))');
 ELSIF position('(environment_workload_retirement_drain_authorized(OLD) OR NOT environment_workload_ownership_active(OLD)))' IN definition)=0 THEN
  RAISE EXCEPTION 'candidate retirement guard differs from the expected migration contract';
 END IF;
 EXECUTE definition;
 definition:=pg_get_functiondef('guard_environment_workload_serving_traffic()'::regprocedure);
 IF position('managed.scope=OLD.scope AND managed.environment_workload_runtime IS NOT NULL' IN definition)>0 THEN
  definition:=replace(definition,
   'managed.scope=OLD.scope AND managed.environment_workload_runtime IS NOT NULL',
   'managed.scope=OLD.scope AND environment_workload_ownership_active(managed)');
 ELSIF position('managed.scope=OLD.scope AND environment_workload_ownership_active(managed)' IN definition)=0 THEN
  RAISE EXCEPTION 'serving ownership guard differs from the expected migration contract';
 END IF;
 EXECUTE definition;
END;
$handoff$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $handoff$
DECLARE definition text;
BEGIN
 definition:=pg_get_functiondef('guard_environment_workload_candidate()'::regprocedure);
 definition:=replace(definition,
  'd.scope=NEW.scope AND environment_workload_ownership_active(d)',
  'd.scope=NEW.scope AND d.environment_workload_runtime IS NOT NULL');
 definition:=replace(definition,
  '(environment_workload_retirement_drain_authorized(OLD) OR NOT environment_workload_ownership_active(OLD)))',
  'environment_workload_retirement_drain_authorized(OLD))');
 EXECUTE definition;
 definition:=pg_get_functiondef('guard_environment_workload_serving_traffic()'::regprocedure);
 EXECUTE replace(definition,
  'managed.scope=OLD.scope AND environment_workload_ownership_active(managed)',
  'managed.scope=OLD.scope AND managed.environment_workload_runtime IS NOT NULL');
END;
$handoff$;
-- +goose StatementEnd
DROP FUNCTION environment_workload_ownership_active(deployments);
