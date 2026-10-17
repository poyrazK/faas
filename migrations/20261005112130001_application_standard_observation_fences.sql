-- filename: 20261005112130001_application_standard_observation_fences.sql
-- adr: 581. Observers lock authoritative membership and application children.
-- Existing migration bytes and immutable native history remain unchanged.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_observation_membership_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' AND NEW.role IS NOT DISTINCT FROM OLD.role THEN RETURN NEW; END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.consumer-membership',0)) THEN
  RAISE EXCEPTION 'application standard membership is busy' USING ERRCODE='55P03';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_observation_membership_guard
 BEFORE INSERT OR DELETE OR UPDATE OF role ON compute_nodes
 FOR EACH ROW EXECUTE FUNCTION application_standard_observation_membership_guard();

CREATE FUNCTION application_standard_observation_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE bodies jsonb[]; body jsonb; app_id uuid; ids uuid[] := '{}';
BEGIN
 IF TG_OP='INSERT' THEN bodies:=ARRAY[to_jsonb(NEW)];
 ELSIF TG_OP='DELETE' THEN bodies:=ARRAY[to_jsonb(OLD)];
 ELSE bodies:=ARRAY[to_jsonb(OLD),to_jsonb(NEW)]; END IF;
 FOREACH body IN ARRAY bodies LOOP
  IF TG_TABLE_NAME='snapshots' THEN
   SELECT d.app_id INTO app_id FROM deployments d WHERE d.id=(body->>'deployment_id')::uuid;
  ELSE app_id:=(body->>'app_id')::uuid; END IF;
  IF app_id IS NOT NULL THEN ids:=array_append(ids,app_id); END IF;
 END LOOP;
 -- Both sides of a move participate. Stable order and NOWAIT avoid cycles with
 -- schedd's instance locks; the observer retries instead of taking a stale read.
 PERFORM a.id FROM apps a WHERE a.id=ANY(ids) ORDER BY a.id FOR SHARE NOWAIT;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_observation_instance_guard
 BEFORE INSERT OR UPDATE OR DELETE ON instances
 FOR EACH ROW EXECUTE FUNCTION application_standard_observation_child_guard();
CREATE TRIGGER application_standard_observation_deployment_guard
 BEFORE INSERT OR UPDATE OR DELETE ON deployments
 FOR EACH ROW EXECUTE FUNCTION application_standard_observation_child_guard();
CREATE TRIGGER application_standard_observation_snapshot_guard
 BEFORE INSERT OR UPDATE OR DELETE ON snapshots
 FOR EACH ROW EXECUTE FUNCTION application_standard_observation_child_guard();

CREATE FUNCTION application_standard_lock_observation(application_id uuid, organization_id uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE locked uuid;
BEGIN
 IF NOT pg_try_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.consumer-membership',0)) THEN
  RAISE EXCEPTION 'application standard membership is busy' USING ERRCODE='55P03';
 END IF;
 SELECT a.id INTO locked FROM apps a JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=application_id AND a.org_id=organization_id AND a.status<>'deleted'
 FOR UPDATE OF a,e NOWAIT;
 IF NOT FOUND THEN RETURN NULL; END IF;
 PERFORM n.id FROM compute_nodes n ORDER BY n.id FOR SHARE NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c ORDER BY c.node_id FOR SHARE NOWAIT;
 -- Includes absent reports, in-flight instances, parked cache and retained
 -- deployments. Child guards fence insertion as well as existing-row changes.
 PERFORM i.id FROM instances i WHERE i.app_id=application_id ORDER BY i.id FOR SHARE NOWAIT;
 PERFORM d.id FROM deployments d WHERE d.app_id=application_id ORDER BY d.id FOR SHARE NOWAIT;
 PERFORM s.id FROM snapshots s JOIN deployments d ON d.id=s.deployment_id
 WHERE d.app_id=application_id ORDER BY s.id FOR SHARE OF s NOWAIT;
 RETURN locked;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION application_standard_lock_observation(uuid,uuid);
DROP TRIGGER application_standard_observation_snapshot_guard ON snapshots;
DROP TRIGGER application_standard_observation_deployment_guard ON deployments;
DROP TRIGGER application_standard_observation_instance_guard ON instances;
DROP FUNCTION application_standard_observation_child_guard();
DROP TRIGGER application_standard_observation_membership_guard ON compute_nodes;
DROP FUNCTION application_standard_observation_membership_guard();
-- +goose StatementEnd
