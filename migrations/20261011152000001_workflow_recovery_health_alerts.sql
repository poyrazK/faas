-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF to_regclass('workflow_recovery_health_incidents') IS NOT NULL AND to_regclass('workflow_recovery_policies_health_idx') IS NOT NULL THEN
  RETURN;
 END IF;
ALTER TABLE workflow_recovery_policies ADD COLUMN next_health_at timestamptz NOT NULL DEFAULT now() CHECK(isfinite(next_health_at));
CREATE INDEX workflow_recovery_policies_health_idx ON workflow_recovery_policies(next_health_at,app_id);
CREATE TABLE workflow_recovery_health_incidents (
 app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
 incident jsonb NOT NULL CHECK(
  jsonb_typeof(incident)='object'
  AND (jsonb_typeof(incident->'episode_id')='string') IS TRUE
  AND (jsonb_typeof(incident->'opened_at')='string') IS TRUE
  AND (jsonb_typeof(incident->'candidate_since')='string') IS TRUE
  AND (jsonb_typeof(incident->'observed_at')='string') IS TRUE
  AND (incident->>'status' IN ('healthy','inactive','paused','blocked','stalled','unknown')) IS TRUE
  AND (jsonb_typeof(incident->'reason')='string') IS TRUE
  AND (NOT incident ? 'closed_at' OR jsonb_typeof(incident->'closed_at')='string')
  AND (NOT incident ? 'alert_notification' OR (jsonb_typeof(incident->'alert_notification')='object' AND (incident->'alert_notification'->>'status' IN ('queued','no_receivers')) IS TRUE))
  AND (NOT incident ? 'recovery_notification' OR (incident ? 'closed_at' AND incident ? 'alert_notification' AND jsonb_typeof(incident->'recovery_notification')='object' AND (incident->'recovery_notification'->>'status' IN ('queued','no_receivers')) IS TRUE))
 )
);
DO $$ DECLARE definition text; BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L))',substring(definition from 8 for length(definition)-8),'automation.recovery_health_alert','automation.recovery_health_recovered');
END $$;
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ DECLARE definition text; BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_health_incidents)
 OR EXISTS(SELECT 1 FROM workflow_recovery_policies WHERE policy ? 'health_alerts')
 OR EXISTS(SELECT 1 FROM app_webhook_event_outbox WHERE event IN ('automation.recovery_health_alert','automation.recovery_health_recovered'))
 OR EXISTS(SELECT 1 FROM app_webhook_deliveries WHERE event IN ('automation.recovery_health_alert','automation.recovery_health_recovered')) THEN
 RAISE EXCEPTION 'cannot discard recovery health alert configuration or notification evidence'; END IF;
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) AND event NOT IN (%L,%L))',substring(definition from 8 for length(definition)-8),'automation.recovery_health_alert','automation.recovery_health_recovered');
END $$;
-- +goose StatementEnd
DROP TABLE workflow_recovery_health_incidents;
DROP INDEX workflow_recovery_policies_health_idx;
ALTER TABLE workflow_recovery_policies DROP COLUMN next_health_at;
