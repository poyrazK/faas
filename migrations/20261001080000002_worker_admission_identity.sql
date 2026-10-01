-- filename: 20261001080000002_worker_admission_identity.sql

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_worker_admission_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF (OLD.mode = 'worker' OR NEW.mode = 'worker') AND OLD.mode IS DISTINCT FROM NEW.mode THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      CONSTRAINT = 'instances_worker_admission_identity',
      MESSAGE = 'worker mode is immutable; create a reserved instance';
  END IF;
  IF NEW.mode = 'worker'
     AND OLD.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
     AND NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm') THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      CONSTRAINT = 'instances_worker_admission_identity',
      MESSAGE = 'worker residency requires a newly reserved instance';
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS instances_worker_admission_identity ON instances;
CREATE TRIGGER instances_worker_admission_identity
BEFORE UPDATE OF state, mode ON instances
FOR EACH ROW EXECUTE FUNCTION guard_worker_admission_identity();

-- +goose Down
DROP TRIGGER IF EXISTS instances_worker_admission_identity ON instances;
DROP FUNCTION IF EXISTS guard_worker_admission_identity();
