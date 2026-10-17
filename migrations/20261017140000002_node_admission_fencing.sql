-- filename: 20261017140000002_node_admission_fencing.sql

-- +goose Up
-- Quiesce old schedd admission/migration dispatchers before applying (ADR-1528).
-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.node_admission_lock_capacity()',current_schema())) IS NULL THEN
CREATE FUNCTION node_admission_lock_capacity() RETURNS void LANGUAGE plpgsql AS $$
DECLARE is_enabled boolean;
BEGIN
 LOOP
  SELECT enabled INTO is_enabled FROM service_capacity_policy WHERE singleton AND enabled FOR UPDATE;
  EXIT WHEN FOUND;
  SELECT enabled INTO is_enabled FROM service_capacity_policy WHERE singleton AND NOT enabled FOR SHARE;
  EXIT WHEN FOUND;
  IF NOT EXISTS (SELECT 1 FROM service_capacity_policy WHERE singleton) THEN
   RAISE EXCEPTION 'service capacity policy is missing';
  END IF;
 END LOOP;
END $$;
 END IF;
END
$replay$;
-- +goose StatementEnd

-- Existing service_capacity_before statement triggers acquire policy first.
-- The share lock also conflicts with non-key lifecycle updates, unlike an FK.
-- +goose StatementBegin
DO $replay$
BEGIN
 IF to_regprocedure(format('%I.instance_node_admission_guard()',current_schema())) IS NULL THEN
CREATE FUNCTION instance_node_admission_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE current_lifecycle compute_node_lifecycle;
BEGIN
 IF NEW.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN
  RETURN NEW;
 END IF;
 IF TG_OP='UPDATE' AND NEW.node_id IS NOT DISTINCT FROM OLD.node_id
  AND OLD.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN
  RETURN NEW;
 END IF;
 SELECT lifecycle INTO current_lifecycle FROM compute_nodes WHERE id=NEW.node_id FOR SHARE;
 IF NOT FOUND OR current_lifecycle NOT IN ('active','recovering') THEN
  RAISE EXCEPTION 'node is not admitting resident work' USING ERRCODE='23514',CONSTRAINT='instance_node_admission_current';
 END IF;
 RETURN NEW;
END $$;
 END IF;
END
$replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='instances'::regclass AND tgname='instance_node_admission_guard') THEN
CREATE TRIGGER instance_node_admission_guard BEFORE INSERT OR UPDATE OF state,node_id ON instances
 FOR EACH ROW EXECUTE FUNCTION instance_node_admission_guard();
 END IF;
END
$replay$;
-- +goose StatementEnd

-- +goose Down
-- Stop all admission/migration writers before removing enforcement.
DROP TRIGGER instance_node_admission_guard ON instances;
DROP FUNCTION instance_node_admission_guard();
DROP FUNCTION node_admission_lock_capacity();
