-- migration-compat: contract
-- filename: 20261022082411537_release_gate_kinds.sql
--
-- ADR-20261022082305: synthetic and metric_change release gates. Gates gain a
-- synthetic check (deleting the check deletes its gates) and two bounds, and
-- probe results are counted per gate, revision and minute. The kind CHECK is
-- replaced online: the widened copy is added NOT VALID and validated, the old
-- one is dropped, and the copy takes the old name. Every step commits on its
-- own and is guarded, so a rerun after a partial failure converges. The
-- previous release survives every step (it skips gate kinds it does not
-- know), but RENAME CONSTRAINT is a contract statement to migration-lint, so
-- a full release applies this migration.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
ALTER TABLE app_release_gates
  ADD COLUMN IF NOT EXISTS synthetic_check_id uuid,
  ADD COLUMN IF NOT EXISTS max_failure_percent integer,
  ADD COLUMN IF NOT EXISTS max_change_percent integer;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_synthetic_check_id_fkey') THEN
  ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_synthetic_check_id_fkey
    FOREIGN KEY (synthetic_check_id) REFERENCES synthetic_checks(id) ON DELETE CASCADE NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_synthetic_check_id_fkey;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_probe_change_chk') THEN
  ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_probe_change_chk CHECK (
    (kind = 'synthetic') = (synthetic_check_id IS NOT NULL)
    AND (kind = 'synthetic') = (max_failure_percent IS NOT NULL)
    AND (kind = 'metric_change') = (max_change_percent IS NOT NULL)
    AND (kind <> 'synthetic' OR (slo_id IS NULL AND max_burn_rate IS NULL AND max_new_groups IS NULL AND min_events IS NULL
      AND metric_name IS NULL AND comparison IS NULL AND threshold IS NULL))
    AND (kind <> 'metric_change' OR (metric_name IS NOT NULL AND comparison IS NOT NULL AND threshold IS NULL
      AND slo_id IS NULL AND max_burn_rate IS NULL AND max_new_groups IS NULL AND min_events IS NULL))
    AND (max_failure_percent IS NULL OR max_failure_percent BETWEEN 0 AND 100)
    AND (max_change_percent IS NULL OR max_change_percent BETWEEN 1 AND 1000)
  ) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_probe_change_chk;
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_kind_next_chk')
    AND position('synthetic' in pg_get_constraintdef((SELECT oid FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_kind_check'))) = 0 THEN
  ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_kind_next_chk
    CHECK (kind IN ('slo', 'new_errors', 'custom_metric', 'synthetic', 'metric_change')) NOT VALID;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_kind_next_chk') THEN
  ALTER TABLE app_release_gates VALIDATE CONSTRAINT app_release_gates_kind_next_chk;
 END IF;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'app_release_gates'::regclass AND conname = 'app_release_gates_kind_next_chk') THEN
  ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_kind_check;
  ALTER TABLE app_release_gates RENAME CONSTRAINT app_release_gates_kind_next_chk TO app_release_gates_kind_check;
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS app_release_gates_synthetic_check
  ON app_release_gates(synthetic_check_id) WHERE synthetic_check_id IS NOT NULL;
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS release_gate_probe_results (
 gate_id uuid NOT NULL REFERENCES app_release_gates(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
 window_start timestamptz NOT NULL CHECK (date_trunc('minute', window_start) = window_start),
 requests integer NOT NULL CHECK (requests BETWEEN 1 AND 10000),
 failures integer NOT NULL CHECK (failures >= 0 AND failures <= requests),
 PRIMARY KEY (gate_id, deployment_id, window_start)
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS release_gate_probe_results_window ON release_gate_probe_results(window_start);
RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM app_release_gates WHERE kind IN ('synthetic', 'metric_change')) THEN
  RAISE EXCEPTION 'delete synthetic and metric_change release gates before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE IF EXISTS release_gate_probe_results;
DROP INDEX IF EXISTS app_release_gates_synthetic_check;
-- +goose StatementBegin
DO $$ BEGIN
 ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_probe_change_chk;
 ALTER TABLE app_release_gates DROP CONSTRAINT IF EXISTS app_release_gates_kind_check;
 ALTER TABLE app_release_gates ADD CONSTRAINT app_release_gates_kind_check CHECK (kind IN ('slo', 'new_errors', 'custom_metric'));
 ALTER TABLE app_release_gates DROP COLUMN IF EXISTS synthetic_check_id,
   DROP COLUMN IF EXISTS max_failure_percent, DROP COLUMN IF EXISTS max_change_percent;
END $$;
-- +goose StatementEnd
RESET lock_timeout;
