-- filename: 20261001184558948_managed_postgres_cutover_task_fence.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-393: deployment-attached commands can carry migration credentials.
CREATE FUNCTION guard_managed_postgres_app_task_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pinned uuid;
BEGIN
 IF NEW.status NOT IN ('restoring','running') THEN RETURN NEW; END IF;
 IF TG_OP='UPDATE' AND ROW(NEW.app_id,NEW.status) IS NOT DISTINCT FROM ROW(OLD.app_id,OLD.status) THEN
  RETURN NEW;
 END IF;
 -- Same tuple barrier as ordinary instances; old repeatable-read claims
 -- abort rather than admitting commands from a pre-fence snapshot.
 SELECT managed_postgres_admission_cutover_id INTO pinned FROM apps WHERE id=NEW.app_id FOR SHARE;
 IF pinned IS NOT NULL THEN
  RAISE EXCEPTION 'app task admission is fenced by a database cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_admission_fenced';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER managed_postgres_app_task_admission_guard BEFORE INSERT OR UPDATE OF app_id,status ON app_tasks
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_app_task_admission();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM apps WHERE managed_postgres_admission_cutover_id IS NOT NULL) THEN
  RAISE EXCEPTION 'cancel fenced cutovers before rollback' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
 END IF;
END $$;
DROP TRIGGER managed_postgres_app_task_admission_guard ON app_tasks;
DROP FUNCTION guard_managed_postgres_app_task_admission();
-- +goose StatementEnd
