-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_mcp_readiness_scope_chk') THEN RETURN; END IF;
 ALTER TABLE alert_rules ADD COLUMN mcp_readiness_scope text NOT NULL DEFAULT '';
 ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_mcp_readiness_scope_chk CHECK(mcp_readiness_scope='' OR (metric='mcp_readiness_unhealthy' AND app_id IS NOT NULL AND mcp_readiness_scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'));
 EXECUTE $definition$CREATE FUNCTION guard_mcp_readiness_alert_scope() RETURNS trigger LANGUAGE plpgsql AS $body$
 BEGIN
  IF NEW.mcp_readiness_scope IS DISTINCT FROM OLD.mcp_readiness_scope THEN RAISE EXCEPTION 'readiness alert environment cannot change; create a new rule' USING ERRCODE='23514',CONSTRAINT='alert_rules_mcp_readiness_scope_immutable'; END IF;
  RETURN NEW;
 END; $body$$definition$;
 CREATE TRIGGER mcp_readiness_alert_scope_immutable BEFORE UPDATE OF mcp_readiness_scope ON alert_rules FOR EACH ROW EXECUTE FUNCTION guard_mcp_readiness_alert_scope();
END $migration$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $migration$ BEGIN
 IF EXISTS(SELECT 1 FROM alert_rules WHERE mcp_readiness_scope<>'') THEN RAISE EXCEPTION 'archive environment-scoped readiness alert rules before rollback'; END IF;
 DROP TRIGGER mcp_readiness_alert_scope_immutable ON alert_rules;
 DROP FUNCTION guard_mcp_readiness_alert_scope();
 ALTER TABLE alert_rules DROP COLUMN mcp_readiness_scope;
END $migration$;
-- +goose StatementEnd
