-- ADR-568: only the graph activation transaction may release a held candidate.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_hold_state() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.environment_workload_runtime IS NOT NULL AND NOT NEW.environment_workload_held THEN
   RAISE EXCEPTION 'environment workload candidates must start held' USING ERRCODE='23514';
  END IF;
 ELSIF OLD.environment_workload_runtime IS NOT NULL AND
   NEW.environment_workload_held IS DISTINCT FROM OLD.environment_workload_held THEN
  RAISE EXCEPTION 'environment workload hold requires graph activation' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;

-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_hold_state ON deployments;
CREATE TRIGGER guard_environment_workload_hold_state
BEFORE INSERT OR UPDATE ON deployments
FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_hold_state();

-- +goose Down
-- Forward-only until graph activation supplies its transaction-bound proof.
SELECT 1;
