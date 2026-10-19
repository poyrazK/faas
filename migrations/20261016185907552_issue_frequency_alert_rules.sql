-- +goose Up
CREATE TABLE IF NOT EXISTS app_issue_frequency_alert_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision > 0),
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array' AND jsonb_array_length(rules)<=10)
);

CREATE TABLE IF NOT EXISTS issue_frequency_alert_receipts (
 app_id uuid NOT NULL REFERENCES app_issue_frequency_alert_policies(app_id) ON DELETE CASCADE,
 rule_key text NOT NULL CHECK(rule_key ~ '^[a-z0-9][a-z0-9-]{0,47}$'),
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 rule_hash text NOT NULL CHECK(rule_hash ~ '^[a-f0-9]{64}$'),
 last_alert_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,rule_key,issue_id)
);
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_activity'::regclass AND conname='issue_activity_action_check' AND position('frequency_threshold_reached' in pg_get_constraintdef(oid))>0) THEN
  ALTER TABLE issue_activity DROP CONSTRAINT IF EXISTS issue_activity_action_check;
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $issue_replay$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='issue_activity'::regclass AND conname='issue_activity_action_check') THEN
  ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check CHECK ((action = ANY (ARRAY['created'::text, 'assigned'::text, 'resolved'::text, 'reopened'::text, 'ignored'::text, 'regressed'::text, 'impact_threshold_reached'::text, 'frequency_threshold_reached'::text, 'merged'::text, 'split'::text, 'split_rule_created'::text, 'split_rule_updated'::text, 'tracker_linked'::text, 'tracker_link_updated'::text, 'tracker_unlinked'::text])));
 END IF;
END $issue_replay$;
-- +goose StatementEnd
-- +goose StatementBegin

DO $$ DECLARE old_check text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO old_check FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF old_check IS NULL THEN RAISE EXCEPTION 'webhook outbox event constraint missing'; END IF;
 IF position('issue.frequency_threshold_reached' in old_check)>0 THEN RETURN; END IF;
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(old_check from 8 for length(old_check)-8),'issue.frequency_threshold_reached');
END $$;
-- +goose StatementEnd

-- +goose Down
DELETE FROM app_webhook_event_outbox WHERE event='issue.frequency_threshold_reached';
DELETE FROM issue_activity WHERE action='frequency_threshold_reached';
DROP TABLE issue_frequency_alert_receipts;
DROP TABLE app_issue_frequency_alert_policies;
ALTER TABLE issue_activity DROP CONSTRAINT issue_activity_action_check;
ALTER TABLE issue_activity ADD CONSTRAINT issue_activity_action_check CHECK ((action = ANY (ARRAY['created'::text, 'assigned'::text, 'resolved'::text, 'reopened'::text, 'ignored'::text, 'regressed'::text, 'impact_threshold_reached'::text, 'merged'::text, 'split'::text, 'split_rule_created'::text, 'split_rule_updated'::text, 'tracker_linked'::text, 'tracker_link_updated'::text, 'tracker_unlinked'::text])));
-- +goose StatementBegin
DO $$ DECLARE old_check text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO old_check FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF old_check IS NULL THEN RAISE EXCEPTION 'webhook outbox event constraint missing'; END IF;
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event<>%L)',substring(old_check from 8 for length(old_check)-8),'issue.frequency_threshold_reached');
END $$;
-- +goose StatementEnd
