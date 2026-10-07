-- filename: 20261004181612764_application_standard_consumer_roster.sql
-- +goose Up
-- +goose StatementBegin
ALTER TABLE application_standard_log_consumers ADD COLUMN stopped_at timestamptz
 CHECK(stopped_at IS NULL OR stopped_at>'epoch'::timestamptz);
CREATE OR REPLACE FUNCTION application_standard_log_consumer_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM n.id FROM compute_nodes n WHERE n.id=NEW.node_id FOR SHARE NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'logging consumer node is absent' USING ERRCODE='55000'; END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW.node_id<>OLD.node_id OR NEW.generation<>OLD.generation+(CASE WHEN NEW.session_id=OLD.session_id THEN 0 ELSE 1 END) THEN
   RAISE EXCEPTION 'logging consumer generation changed' USING ERRCODE='40001';
  END IF;
  IF NEW.session_id=OLD.session_id THEN
   NEW.registered_at:=OLD.registered_at;
   IF OLD.stopped_at IS NOT NULL AND NEW.stopped_at IS NULL THEN
    RAISE EXCEPTION 'logging consumer session is stopped' USING ERRCODE='55000';
   END IF;
   IF NEW.stopped_at IS NOT NULL THEN
    NEW.stopped_at:=coalesce(OLD.stopped_at,clock_timestamp());
    RETURN NEW;
   END IF;
  END IF;
 END IF;
 IF NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=NEW.node_id AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'logging consumer node is unavailable' USING ERRCODE='40001';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.generation<>1 OR NEW.stopped_at IS NOT NULL THEN
   RAISE EXCEPTION 'invalid logging consumer generation' USING ERRCODE='40001';
  END IF;
  NEW.registered_at:=clock_timestamp();
 ELSIF NEW.session_id<>OLD.session_id THEN
  IF NEW.stopped_at IS NOT NULL THEN RAISE EXCEPTION 'new logging session is stopped' USING ERRCODE='40001'; END IF;
  NEW.registered_at:=clock_timestamp();
 END IF;
 RETURN NEW;
END;
$$;
CREATE FUNCTION application_standard_log_consumer_open_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM c.node_id FROM application_standard_log_consumers c JOIN compute_nodes n ON n.id=c.node_id
 WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation AND c.stopped_at IS NULL
 FOR SHARE OF c,n NOWAIT;
 IF NOT FOUND THEN RAISE EXCEPTION 'logging consumer session is stopped or superseded' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_inventory_consumer_open BEFORE INSERT OR UPDATE ON application_standard_log_inventories
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_consumer_open_guard();
CREATE TRIGGER application_standard_log_health_consumer_open BEFORE INSERT OR UPDATE ON application_standard_log_health
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_consumer_open_guard();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER application_standard_log_inventory_consumer_open ON application_standard_log_inventories;
DROP TRIGGER application_standard_log_health_consumer_open ON application_standard_log_health;
DROP FUNCTION application_standard_log_consumer_open_guard();
ALTER TABLE application_standard_log_consumers DROP COLUMN stopped_at;
CREATE OR REPLACE FUNCTION application_standard_log_consumer_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM compute_nodes n WHERE n.id=NEW.node_id AND n.active AND n.role IS DISTINCT FROM 'control-plane') THEN
  RAISE EXCEPTION 'logging consumer node is unavailable' USING ERRCODE='40001';
 END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.generation<>1 THEN RAISE EXCEPTION 'invalid logging consumer generation' USING ERRCODE='40001'; END IF;
  NEW.registered_at:=clock_timestamp();
 ELSE
  IF NEW.node_id<>OLD.node_id OR NEW.generation<>OLD.generation+(CASE WHEN NEW.session_id=OLD.session_id THEN 0 ELSE 1 END) THEN
   RAISE EXCEPTION 'logging consumer generation changed' USING ERRCODE='40001';
  END IF;
  NEW.registered_at:=CASE WHEN NEW.session_id=OLD.session_id THEN OLD.registered_at ELSE clock_timestamp() END;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
