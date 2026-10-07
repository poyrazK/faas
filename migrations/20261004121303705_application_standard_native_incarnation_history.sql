-- filename: 20261004121303705_application_standard_native_incarnation_history.sql
-- ADR-435: native startup identities cannot be reactivated by late registration.
-- +goose Up
-- +goose StatementBegin
LOCK TABLE compute_nodes IN SHARE ROW EXCLUSIVE MODE;
CREATE TABLE application_standard_native_incarnations (
 node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
 incarnation uuid NOT NULL CHECK(incarnation<>'00000000-0000-0000-0000-000000000000'::uuid),
 protocol_version smallint NOT NULL CHECK(protocol_version IN (1,2,3)),
 registered_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(registered_at>'epoch'::timestamptz),
 PRIMARY KEY(node_id,incarnation)
);
INSERT INTO application_standard_native_incarnations(node_id,incarnation,protocol_version)
 SELECT id,vmmd_incarnation,vmmd_admission_protocol FROM compute_nodes WHERE vmmd_incarnation IS NOT NULL;
CREATE FUNCTION application_standard_native_incarnation_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND OLD.vmmd_incarnation IS NOT NULL THEN
  IF NEW.vmmd_incarnation IS NULL OR (NEW.vmmd_incarnation=OLD.vmmd_incarnation AND NEW.vmmd_admission_protocol<>OLD.vmmd_admission_protocol) THEN
   RAISE EXCEPTION 'native process identity is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 IF NEW.vmmd_incarnation IS NOT NULL AND EXISTS(
  SELECT 1 FROM application_standard_native_incarnations h WHERE h.node_id=NEW.id AND h.incarnation=NEW.vmmd_incarnation
 ) THEN
  IF TG_OP='INSERT' OR NEW.vmmd_incarnation IS DISTINCT FROM OLD.vmmd_incarnation THEN
   RAISE EXCEPTION 'native process identity was superseded' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_native_incarnation_once BEFORE INSERT OR UPDATE OF vmmd_incarnation,vmmd_admission_protocol ON compute_nodes
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_incarnation_once();
CREATE FUNCTION application_standard_native_incarnation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM compute_nodes WHERE id=OLD.node_id) THEN RETURN OLD; END IF;
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'native process history is immutable' USING ERRCODE='55000';
 END IF;
 IF NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=NEW.node_id AND n.vmmd_incarnation=NEW.incarnation AND n.vmmd_admission_protocol=NEW.protocol_version) THEN
  RAISE EXCEPTION 'native process history must name current identity' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 NEW.registered_at:=clock_timestamp();
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_native_incarnation_immutable BEFORE INSERT OR UPDATE OR DELETE ON application_standard_native_incarnations
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_incarnation_guard();
CREATE FUNCTION application_standard_native_incarnation_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.vmmd_incarnation IS NOT NULL THEN
  INSERT INTO application_standard_native_incarnations(node_id,incarnation,protocol_version)
   VALUES(NEW.id,NEW.vmmd_incarnation,NEW.vmmd_admission_protocol) ON CONFLICT(node_id,incarnation) DO NOTHING;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_native_incarnation_capture AFTER INSERT OR UPDATE OF vmmd_incarnation,vmmd_admission_protocol ON compute_nodes
 FOR EACH ROW EXECUTE FUNCTION application_standard_native_incarnation_capture();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER application_standard_native_incarnation_capture ON compute_nodes;
DROP FUNCTION application_standard_native_incarnation_capture();
DROP TRIGGER application_standard_native_incarnation_once ON compute_nodes;
DROP FUNCTION application_standard_native_incarnation_once();
DROP TABLE application_standard_native_incarnations;
DROP FUNCTION application_standard_native_incarnation_guard();
-- +goose StatementEnd
