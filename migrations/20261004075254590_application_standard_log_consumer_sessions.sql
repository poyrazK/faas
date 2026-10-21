-- filename: 20261004075254590_application_standard_log_consumer_sessions.sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE application_standard_log_consumer_sessions (
 node_id uuid NOT NULL REFERENCES compute_nodes(id) ON DELETE CASCADE,
 session_id uuid NOT NULL CHECK(session_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 generation bigint NOT NULL CHECK(generation>0),
 registered_at timestamptz NOT NULL CHECK(registered_at>'epoch'::timestamptz),
 PRIMARY KEY(node_id,session_id), UNIQUE(node_id,generation)
);
INSERT INTO application_standard_log_consumer_sessions SELECT node_id,session_id,generation,registered_at FROM application_standard_log_consumers;
CREATE FUNCTION application_standard_log_consumer_session_once() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF EXISTS(SELECT 1 FROM compute_nodes WHERE id=OLD.node_id) THEN
   RAISE EXCEPTION 'logging consumer lineage is retained' USING ERRCODE='55000';
  END IF;
  RETURN OLD;
 END IF;
 IF EXISTS(SELECT 1 FROM application_standard_log_consumer_sessions h WHERE h.node_id=NEW.node_id AND h.session_id=NEW.session_id)
 AND NOT EXISTS(SELECT 1 FROM application_standard_log_consumers c WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id) THEN
  RAISE EXCEPTION 'logging consumer session is superseded' USING ERRCODE='55000';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_consumer_session_once BEFORE INSERT OR UPDATE OR DELETE ON application_standard_log_consumers
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_consumer_session_once();
CREATE FUNCTION application_standard_log_consumer_session_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' AND NOT EXISTS(SELECT 1 FROM compute_nodes WHERE id=OLD.node_id) THEN RETURN OLD; END IF;
 IF TG_OP<>'INSERT' THEN
  RAISE EXCEPTION 'logging consumer session history is immutable' USING ERRCODE='55000';
 END IF;
 SELECT c.registered_at INTO NEW.registered_at FROM application_standard_log_consumers c
 WHERE c.node_id=NEW.node_id AND c.session_id=NEW.session_id AND c.generation=NEW.generation;
 IF NOT FOUND THEN RAISE EXCEPTION 'logging consumer session history is not current' USING ERRCODE='55000'; END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_consumer_session_immutable BEFORE INSERT OR UPDATE OR DELETE ON application_standard_log_consumer_sessions
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_consumer_session_guard();
CREATE FUNCTION application_standard_log_consumer_session_capture() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO application_standard_log_consumer_sessions(node_id,session_id,generation,registered_at)
 VALUES(NEW.node_id,NEW.session_id,NEW.generation,NEW.registered_at) ON CONFLICT(node_id,session_id) DO NOTHING;
 RETURN NEW;
END;
$$;
CREATE TRIGGER application_standard_log_consumer_session_capture AFTER INSERT OR UPDATE ON application_standard_log_consumers
 FOR EACH ROW EXECUTE FUNCTION application_standard_log_consumer_session_capture();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER application_standard_log_consumer_session_capture ON application_standard_log_consumers;
DROP FUNCTION application_standard_log_consumer_session_capture();
DROP TRIGGER application_standard_log_consumer_session_once ON application_standard_log_consumers;
DROP FUNCTION application_standard_log_consumer_session_once();
DROP TABLE application_standard_log_consumer_sessions;
DROP FUNCTION application_standard_log_consumer_session_guard();
-- +goose StatementEnd
