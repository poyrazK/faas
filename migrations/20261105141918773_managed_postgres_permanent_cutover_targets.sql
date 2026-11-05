-- filename: 20261105141918773_managed_postgres_permanent_cutover_targets.sql
-- migration-compat: expand

-- A temporary restore must never become the app's permanent database.
-- The catalog guard also protects workers from N-1 during a rolling release.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
-- +goose StatementBegin
BEGIN;
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION guard_managed_postgres_permanent_cutover_target() RETURNS trigger
 LANGUAGE plpgsql AS $$
DECLARE deadline timestamptz;
BEGIN
 IF (TG_OP='INSERT' AND NEW.state NOT IN ('cancelling','cancelled'))
  OR (TG_OP='UPDATE' AND NEW.state='activated' AND OLD.state IS DISTINCT FROM NEW.state) THEN
  SELECT restore_slot_until INTO deadline FROM managed_postgres_databases
   WHERE id=NEW.target_database_id FOR SHARE;
  IF deadline IS NOT NULL THEN
   RAISE EXCEPTION 'a temporary restore cannot be a cutover target'
    USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER managed_postgres_permanent_cutover_target
 BEFORE INSERT OR UPDATE OF state, target_database_id ON managed_postgres_cutovers
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_permanent_cutover_target();
COMMIT;
-- +goose StatementEnd

-- Activation is irreversible. Preserve any still-live target an older
-- worker already activated rather than auto-deleting it when its slot ends.
-- Ordinary plan limits continue to apply; removing the old source frees room.
-- +goose StatementBegin
DO $$
DECLARE changed integer;
BEGIN
 LOOP
  WITH batch AS (
   SELECT d.id FROM managed_postgres_databases d
    WHERE d.restore_slot_until IS NOT NULL AND d.state NOT IN ('deleting','deleted')
     AND EXISTS (SELECT 1 FROM managed_postgres_cutovers c WHERE c.target_database_id=d.id AND c.state='activated')
    ORDER BY d.id LIMIT 100
  )
  UPDATE managed_postgres_databases d SET restore_slot_until=NULL,updated_at=clock_timestamp()
   FROM batch WHERE d.id=batch.id;
  GET DIAGNOSTICS changed = ROW_COUNT;
  COMMIT;
  EXIT WHEN changed=0;
 END LOOP;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
BEGIN;
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS managed_postgres_permanent_cutover_target ON managed_postgres_cutovers;
DROP FUNCTION IF EXISTS guard_managed_postgres_permanent_cutover_target();
-- Do not restore an expiry deadline on a database already serving an app.
COMMIT;
-- +goose StatementEnd
