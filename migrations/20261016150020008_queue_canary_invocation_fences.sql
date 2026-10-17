-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_queue_canary_invocation_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE affected_app uuid;
BEGIN
 IF ROW(OLD.state,OLD.attempts,OLD.replay_generation,OLD.received_at,OLD.completed_at,OLD.headers,OLD.queue_binding_id,OLD.deployment_scope,OLD.deadline_at,OLD.account_id,OLD.app_id)
 IS NOT DISTINCT FROM ROW(NEW.state,NEW.attempts,NEW.replay_generation,NEW.received_at,NEW.completed_at,NEW.headers,NEW.queue_binding_id,NEW.deployment_scope,NEW.deadline_at,NEW.account_id,NEW.app_id) THEN RETURN NEW; END IF;
 SELECT app_id INTO affected_app FROM queue_binding_canaries WHERE id=NEW.id;
 IF affected_app IS NOT NULL THEN PERFORM bump_binding_promotion_revision(affected_app); END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='invocations'::regclass AND tgname='queue_canary_invocation_revision') THEN
  CREATE TRIGGER queue_canary_invocation_revision AFTER UPDATE OF state,attempts,replay_generation,received_at,completed_at,headers,queue_binding_id,deployment_scope,deadline_at,account_id,app_id
ON invocations FOR EACH ROW EXECUTE FUNCTION capture_queue_canary_invocation_revision();
 END IF;
END $replay$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER queue_canary_invocation_revision ON invocations;
DROP FUNCTION capture_queue_canary_invocation_revision();
