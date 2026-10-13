-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS event_execution_coverage (
 subscription_id uuid PRIMARY KEY,
 account_id uuid NOT NULL,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 started_at timestamptz NOT NULL,
 baseline_known boolean NOT NULL,
 lost_at timestamptz,
 loss_reason text NOT NULL DEFAULT '' CHECK (loss_reason IN ('','receipt_deleted','recipient_deleted','invocation_deleted','attempt_deleted','execution_replaced')),
 CHECK ((lost_at IS NULL)=(loss_reason=''))
);
CREATE INDEX IF NOT EXISTS event_execution_coverage_app_idx ON event_execution_coverage(app_id);
CREATE INDEX IF NOT EXISTS event_execution_attempt_window_idx ON invocation_attempt_history(account_id,app_id,finished_at) INCLUDE (retain_until) WHERE finished_at IS NOT NULL;
-- Existing subscriptions may have lost associations before instrumentation.
INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known)
 SELECT id,account_id,app_id,clock_timestamp(),false FROM event_subscriptions
 ON CONFLICT DO NOTHING;
INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known)
 SELECT subscription_id,account_id,app_id,clock_timestamp(),false FROM event_subscription_delivery_controls
 ON CONFLICT DO NOTHING;
CREATE OR REPLACE FUNCTION start_event_execution_coverage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO event_execution_coverage(subscription_id,account_id,app_id,started_at,baseline_known)
 VALUES(NEW.id,NEW.account_id,NEW.app_id,clock_timestamp(),true) ON CONFLICT DO NOTHING;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS start_event_execution_coverage ON event_subscriptions;
CREATE TRIGGER start_event_execution_coverage AFTER INSERT ON event_subscriptions
 FOR EACH ROW EXECUTE FUNCTION start_event_execution_coverage();
CREATE OR REPLACE FUNCTION lose_event_execution_coverage() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_TABLE_NAME='event_fanout_outbox' THEN
  UPDATE event_execution_coverage c SET lost_at=clock_timestamp(),loss_reason='receipt_deleted'
  WHERE c.lost_at IS NULL AND (
   EXISTS (SELECT 1 FROM jsonb_array_elements(coalesce(OLD.recipient_snapshot,'[]'::jsonb)) r WHERE r->>'id'=c.subscription_id::text)
   OR EXISTS (SELECT 1 FROM event_fanout_recipients r WHERE r.outbox_id=OLD.id AND r.subscription_id=c.subscription_id::text));
 ELSIF TG_TABLE_NAME='event_fanout_recipients' THEN
  UPDATE event_execution_coverage SET lost_at=clock_timestamp(),loss_reason='recipient_deleted'
  WHERE lost_at IS NULL AND subscription_id::text=OLD.subscription_id;
 ELSE
  IF TG_OP='UPDATE' THEN
   IF OLD.replay_generation IS NOT DISTINCT FROM NEW.replay_generation
   AND OLD.created_at IS NOT DISTINCT FROM NEW.created_at AND OLD.app_id IS NOT DISTINCT FROM NEW.app_id
   AND OLD.account_id IS NOT DISTINCT FROM NEW.account_id
   AND OLD.replay_root_invocation_id IS NOT DISTINCT FROM NEW.replay_root_invocation_id
   AND OLD.replayed_from_invocation_id IS NOT DISTINCT FROM NEW.replayed_from_invocation_id
   AND (OLD.state<>'completed' OR (NEW.state='completed' AND OLD.completed_at IS NOT DISTINCT FROM NEW.completed_at)) THEN
    RETURN NEW;
   END IF;
  END IF;
  -- Lost invocation associations cannot reliably be mapped to one subscription.
  UPDATE event_execution_coverage SET lost_at=clock_timestamp(),
   loss_reason=CASE WHEN TG_OP='UPDATE' THEN 'execution_replaced'
    WHEN TG_TABLE_NAME='invocations' THEN 'invocation_deleted' ELSE 'attempt_deleted' END
  WHERE lost_at IS NULL AND app_id=OLD.app_id;
 END IF;
 IF TG_OP='UPDATE' THEN RETURN NEW; END IF;
 RETURN OLD;
END $$;
DROP TRIGGER IF EXISTS event_execution_receipt_loss ON event_fanout_outbox;
CREATE TRIGGER event_execution_receipt_loss BEFORE DELETE ON event_fanout_outbox
 FOR EACH ROW EXECUTE FUNCTION lose_event_execution_coverage();
DROP TRIGGER IF EXISTS event_execution_recipient_loss ON event_fanout_recipients;
CREATE TRIGGER event_execution_recipient_loss BEFORE DELETE ON event_fanout_recipients
 FOR EACH ROW EXECUTE FUNCTION lose_event_execution_coverage();
DROP TRIGGER IF EXISTS event_execution_invocation_loss ON invocations;
CREATE TRIGGER event_execution_invocation_loss BEFORE DELETE OR UPDATE ON invocations
 FOR EACH ROW EXECUTE FUNCTION lose_event_execution_coverage();
DROP TRIGGER IF EXISTS event_execution_attempt_loss ON invocation_attempt_history;
CREATE TRIGGER event_execution_attempt_loss BEFORE DELETE ON invocation_attempt_history
 FOR EACH ROW EXECUTE FUNCTION lose_event_execution_coverage();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER event_execution_attempt_loss ON invocation_attempt_history;
DROP TRIGGER event_execution_invocation_loss ON invocations;
DROP TRIGGER event_execution_recipient_loss ON event_fanout_recipients;
DROP TRIGGER event_execution_receipt_loss ON event_fanout_outbox;
DROP TRIGGER start_event_execution_coverage ON event_subscriptions;
DROP FUNCTION lose_event_execution_coverage();
DROP FUNCTION start_event_execution_coverage();
DROP TABLE event_execution_coverage;
DROP INDEX event_execution_attempt_window_idx;
-- +goose StatementEnd
