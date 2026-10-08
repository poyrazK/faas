-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION valid_event_routing_retry_policy(p jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN p IS NULL THEN true
 WHEN jsonb_typeof(p)<>'object' OR NOT (p ?& ARRAY['max_attempts','max_retry_duration_ms','initial_backoff_ms','max_backoff_ms','jitter']) THEN false
 WHEN jsonb_typeof(p->'max_attempts')<>'number' OR jsonb_typeof(p->'max_retry_duration_ms')<>'number' OR jsonb_typeof(p->'initial_backoff_ms')<>'number' OR jsonb_typeof(p->'max_backoff_ms')<>'number' OR jsonb_typeof(p->'jitter')<>'boolean' THEN false
 WHEN p ? 'max_delivery_age_ms' AND jsonb_typeof(p->'max_delivery_age_ms')<>'number' THEN false
 ELSE coalesce((p->>'max_delivery_age_ms')::numeric,0) BETWEEN 0 AND 2592000000 AND coalesce((p->>'max_delivery_age_ms')::numeric,0)%1=0
 AND (p->>'max_attempts')::numeric BETWEEN 1 AND 100 AND (p->>'max_attempts')::numeric % 1=0
 AND (p->>'max_retry_duration_ms')::numeric BETWEEN 0 AND 604800000 AND (p->>'max_retry_duration_ms')::numeric % 1=0
 AND (p->>'initial_backoff_ms')::numeric BETWEEN 1 AND 3600000 AND (p->>'initial_backoff_ms')::numeric % 1=0
 AND (p->>'max_backoff_ms')::numeric BETWEEN (p->>'initial_backoff_ms')::numeric AND 3600000 AND (p->>'max_backoff_ms')::numeric % 1=0 END;
$$;
CREATE FUNCTION event_recipient_delivery_deadline(recipient jsonb, accepted_at timestamptz, progress jsonb)
RETURNS timestamptz LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN recipient ? 'workflow' OR recipient ? 'object_notification'
 OR coalesce((recipient->'work'->>'ordered')::boolean,false)
 OR coalesce((progress->>'delivery_age_override')::boolean,false)
 OR coalesce((recipient->>'delivery_age_override')::boolean,false)
 OR coalesce((recipient->'routing_retry_policy'->>'max_delivery_age_ms')::bigint,0)=0 THEN NULL
 ELSE accepted_at + ((recipient->'routing_retry_policy'->>'max_delivery_age_ms')::bigint * interval '1 millisecond') END;
$$;
ALTER TABLE event_fanout_recipients ADD COLUMN delivery_deadline_at timestamptz CHECK(delivery_deadline_at IS NULL OR isfinite(delivery_deadline_at));
CREATE INDEX event_recipient_delivery_deadline_idx ON event_fanout_recipients(delivery_deadline_at,outbox_id) WHERE state IN ('pending','processing') AND delivery_deadline_at IS NOT NULL;
ALTER TABLE event_fanout_attempt_history DROP CONSTRAINT event_fanout_attempt_history_retry_stop_reason_check;
ALTER TABLE event_fanout_attempt_history ADD CONSTRAINT event_fanout_attempt_history_retry_stop_reason_check CHECK(retry_stop_reason IN ('','non_retryable','max_attempts','max_duration','delivery_expired'));
CREATE FUNCTION enforce_event_delivery_age_ordering() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE p jsonb;
BEGIN
 IF TG_TABLE_NAME='event_subscriptions' THEN
  IF coalesce((NEW.routing_retry_policy->>'max_delivery_age_ms')::bigint,0)>0
   AND EXISTS(SELECT 1 FROM event_subscription_work_bindings b WHERE b.subscription_id=NEW.id AND b.ordered) THEN
   RAISE EXCEPTION 'ordered event delivery cannot configure max_delivery_age' USING ERRCODE='23514';
  END IF;
 ELSIF NEW.ordered THEN
  SELECT routing_retry_policy INTO p FROM event_subscriptions WHERE id=NEW.subscription_id FOR UPDATE;
  IF coalesce((p->>'max_delivery_age_ms')::bigint,0)>0 THEN
   RAISE EXCEPTION 'ordered event delivery cannot configure max_delivery_age' USING ERRCODE='23514';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER event_delivery_age_subscription_ordering BEFORE UPDATE OF routing_retry_policy ON event_subscriptions FOR EACH ROW EXECUTE FUNCTION enforce_event_delivery_age_ordering();
CREATE TRIGGER event_delivery_age_binding_ordering BEFORE INSERT OR UPDATE ON event_subscription_work_bindings FOR EACH ROW EXECUTE FUNCTION enforce_event_delivery_age_ordering();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER event_delivery_age_subscription_ordering ON event_subscriptions;
DROP TRIGGER event_delivery_age_binding_ordering ON event_subscription_work_bindings;
DROP FUNCTION enforce_event_delivery_age_ordering();
ALTER TABLE event_fanout_recipients DROP COLUMN delivery_deadline_at;
DROP FUNCTION event_recipient_delivery_deadline(jsonb,timestamptz,jsonb);
CREATE OR REPLACE FUNCTION valid_event_routing_retry_policy(p jsonb) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
 SELECT CASE WHEN p IS NULL THEN true
 WHEN jsonb_typeof(p)<>'object' OR NOT (p ?& ARRAY['max_attempts','max_retry_duration_ms','initial_backoff_ms','max_backoff_ms','jitter']) THEN false
 WHEN jsonb_typeof(p->'max_attempts')<>'number' OR jsonb_typeof(p->'max_retry_duration_ms')<>'number' OR jsonb_typeof(p->'initial_backoff_ms')<>'number' OR jsonb_typeof(p->'max_backoff_ms')<>'number' OR jsonb_typeof(p->'jitter')<>'boolean' THEN false
 ELSE (p->>'max_attempts')::numeric BETWEEN 1 AND 100 AND (p->>'max_attempts')::numeric % 1=0
 AND (p->>'max_retry_duration_ms')::numeric BETWEEN 0 AND 604800000 AND (p->>'max_retry_duration_ms')::numeric % 1=0
 AND (p->>'initial_backoff_ms')::numeric BETWEEN 1 AND 3600000 AND (p->>'initial_backoff_ms')::numeric % 1=0
 AND (p->>'max_backoff_ms')::numeric BETWEEN (p->>'initial_backoff_ms')::numeric AND 3600000 AND (p->>'max_backoff_ms')::numeric % 1=0 END;
$$;
-- +goose StatementEnd
