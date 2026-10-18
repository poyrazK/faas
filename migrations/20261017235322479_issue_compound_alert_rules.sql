-- +goose Up
-- migration-compat: expand
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS app_issue_compound_alert_policies (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 revision bigint NOT NULL CHECK(revision > 0),
 rules jsonb NOT NULL CHECK(jsonb_typeof(rules)='array' AND jsonb_array_length(rules)<=10)
);
CREATE TABLE IF NOT EXISTS issue_compound_alert_receipts (
 app_id uuid NOT NULL REFERENCES app_issue_compound_alert_policies(app_id) ON DELETE CASCADE,
 rule_key text NOT NULL CHECK(rule_key ~ '^[a-z0-9][a-z0-9-]{0,47}$'),
 issue_id uuid NOT NULL REFERENCES app_issues(id) ON DELETE CASCADE,
 rule_hash text NOT NULL CHECK(rule_hash ~ '^[a-f0-9]{64}$'),
 last_alert_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,rule_key,issue_id)
);
-- +goose StatementBegin
DO $compound$ DECLARE item record; definition text; location integer; BEGIN
 FOR item IN SELECT * FROM (VALUES
  ('issue_activity','issue_activity_action_check','compound_threshold_reached'),
  ('app_webhook_event_outbox','app_webhook_event_outbox_event_chk','issue.compound_threshold_reached'),
  ('issue_application_slack_policies','issue_application_slack_policies_events_check','compound_threshold_reached')
 ) AS x(table_name,constraint_name,event_name) LOOP
  SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
   WHERE conrelid=to_regclass(item.table_name) AND conname=item.constraint_name;
  IF definition IS NULL THEN RAISE EXCEPTION 'missing alert vocabulary constraint: %',item.constraint_name; END IF;
  IF position(quote_literal(item.event_name)||'::text' in definition)>0 THEN CONTINUE; END IF;
  location := position('ARRAY[' in definition);
  IF location=0 THEN RAISE EXCEPTION 'unrecognized alert vocabulary constraint: %',item.constraint_name; END IF;
  definition := overlay(definition placing 'ARRAY['||quote_literal(item.event_name)||'::text, ' from location for 6);
  IF item.table_name='issue_application_slack_policies' THEN
   IF position('cardinality(events) <= 4' in definition)=0 THEN RAISE EXCEPTION 'unrecognized Slack event bound'; END IF;
   definition := replace(definition,'cardinality(events) <= 4','cardinality(events) <= 5');
  END IF;
  EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I',item.table_name,item.constraint_name);
  EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s',item.table_name,item.constraint_name,definition);
 END LOOP;
END $compound$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- Never erase configured rules or retained incident/delivery evidence on rollback.
-- +goose StatementBegin
DO $compound$ DECLARE item record; definition text; BEGIN
 IF EXISTS(SELECT 1 FROM app_issue_compound_alert_policies WHERE rules<>'[]'::jsonb)
 OR EXISTS(SELECT 1 FROM issue_compound_alert_receipts)
 OR EXISTS(SELECT 1 FROM issue_activity WHERE action='compound_threshold_reached')
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event='issue.compound_threshold_reached')
 OR EXISTS(SELECT 1 FROM issue_application_slack_policies WHERE 'compound_threshold_reached'=ANY(events))
 OR EXISTS(SELECT 1 FROM issue_slack_routing_policies p CROSS JOIN LATERAL jsonb_array_elements(p.rules) r WHERE r->>'event'='compound_threshold_reached')
 THEN RAISE EXCEPTION 'compound alert configuration or evidence remains; rollback refused'; END IF;
 FOR item IN SELECT * FROM (VALUES
  ('issue_activity','issue_activity_action_check','compound_threshold_reached'),
  ('app_webhook_event_outbox','app_webhook_event_outbox_event_chk','issue.compound_threshold_reached'),
  ('issue_application_slack_policies','issue_application_slack_policies_events_check','compound_threshold_reached')
 ) AS x(table_name,constraint_name,event_name) LOOP
  SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
   WHERE conrelid=to_regclass(item.table_name) AND conname=item.constraint_name;
  IF definition IS NULL THEN RAISE EXCEPTION 'missing alert vocabulary constraint: %',item.constraint_name; END IF;
  definition := replace(definition,quote_literal(item.event_name)||'::text, ','');
  IF item.table_name='issue_application_slack_policies' THEN
   definition := replace(definition,'cardinality(events) <= 5','cardinality(events) <= 4');
  END IF;
  EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I',item.table_name,item.constraint_name);
  EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I %s',item.table_name,item.constraint_name,definition);
 END LOOP;
END $compound$;
-- +goose StatementEnd
DROP TABLE issue_compound_alert_receipts;
DROP TABLE app_issue_compound_alert_policies;
