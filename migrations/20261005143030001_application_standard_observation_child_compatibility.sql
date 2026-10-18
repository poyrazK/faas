-- filename: 20261005143030001_application_standard_observation_child_compatibility.sql
-- adr: 581. Legacy cleanup must not acquire a reverse application-row lock.
-- The first two applied observation migrations remain unchanged.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_observation_child_guard() RETURNS trigger LANGUAGE plpgsql AS $$
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
 FOR app_id IN SELECT DISTINCT x.id FROM unnest(ids) AS x(id) ORDER BY x.id LOOP
  IF NOT pg_try_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.observation.'||app_id::text,0)) THEN
   RAISE EXCEPTION 'application standard observation is busy' USING ERRCODE='55P03';
  END IF;
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION application_standard_lock_observation(application_id uuid, organization_id uuid) RETURNS uuid LANGUAGE plpgsql AS $$
DECLARE locked uuid;
BEGIN
 IF NOT pg_try_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.consumer-membership',0)) THEN
  RAISE EXCEPTION 'application standard membership is busy' USING ERRCODE='55P03';
 END IF;
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.observation.'||application_id::text,0)) THEN
  RAISE EXCEPTION 'application standard children are busy' USING ERRCODE='55P03';
 END IF;
 SELECT a.id INTO locked FROM apps a JOIN app_application_standards e ON e.app_id=a.id
 WHERE a.id=application_id AND a.org_id=organization_id AND a.status<>'deleted'
 FOR UPDATE OF a,e NOWAIT;
 IF NOT FOUND THEN RETURN NULL; END IF;
 PERFORM n.id FROM compute_nodes n ORDER BY n.id FOR SHARE NOWAIT;
 PERFORM c.node_id FROM application_standard_log_consumers c ORDER BY c.node_id FOR SHARE NOWAIT;
 PERFORM i.id FROM instances i WHERE i.app_id=application_id ORDER BY i.id FOR SHARE NOWAIT;
 PERFORM d.id FROM deployments d WHERE d.app_id=application_id ORDER BY d.id FOR SHARE NOWAIT;
 PERFORM s.id FROM snapshots s JOIN deployments d ON d.id=s.deployment_id
 WHERE d.app_id=application_id ORDER BY s.id FOR SHARE OF s NOWAIT;
 RETURN locked;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Preserve compatible fail-closed observation fences during a binary rollback.
-- The earlier migration removes these functions when the feature is uninstalled.
SELECT 1;
