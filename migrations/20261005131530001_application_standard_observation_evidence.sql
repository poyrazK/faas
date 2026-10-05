-- filename: 20261005131530001_application_standard_observation_evidence.sql
-- adr: 581. Retain parent/control/report fences from qualification to checkpoint.
-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION application_standard_lock_observation_evidence(application_id uuid) RETURNS boolean LANGUAGE plpgsql AS $$
BEGIN
 IF NOT pg_try_advisory_xact_lock(hashtextextended('gregale.application-standard.controls.'||application_id::text,0)) THEN
  RAISE EXCEPTION 'application standard controls are busy' USING ERRCODE='55P03';
 END IF;
 PERFORM a.id FROM apps a JOIN orgs o ON o.id=a.org_id JOIN accounts acct ON acct.id=a.account_id
 WHERE a.id=application_id FOR SHARE OF o,acct NOWAIT;
 PERFORM p.id FROM projects p JOIN apps a ON a.project_id=p.id WHERE a.id=application_id FOR SHARE OF p NOWAIT;
 PERFORM d.id FROM app_log_drains d WHERE d.app_id=application_id ORDER BY d.id FOR SHARE NOWAIT;
 PERFORM b.app_id FROM application_standard_control_bindings b WHERE b.app_id=application_id
 ORDER BY b.field,b.resource_id FOR SHARE NOWAIT;
 PERFORM r.id FROM application_standard_log_destinations r JOIN application_standard_control_bindings b
 ON b.resource_id=r.id AND b.field='log_destinations' WHERE b.app_id=application_id ORDER BY r.id FOR SHARE OF r NOWAIT;
 PERFORM x.app_id FROM application_standard_log_inventories x WHERE x.app_id=application_id ORDER BY x.node_id FOR SHARE NOWAIT;
 PERFORM h.app_id FROM application_standard_log_health h WHERE h.app_id=application_id ORDER BY h.node_id,h.drain_id FOR SHARE NOWAIT;
 PERFORM e.app_id FROM application_standard_egress_observations e WHERE e.app_id=application_id ORDER BY e.node_id FOR SHARE NOWAIT;
 RETURN true;
END;
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION application_standard_lock_observation_evidence(uuid);
