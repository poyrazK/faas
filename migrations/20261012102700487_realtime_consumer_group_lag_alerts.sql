-- +goose Up
CREATE TABLE IF NOT EXISTS managed_realtime_consumer_group_alerts (
 group_id uuid PRIMARY KEY REFERENCES managed_realtime_consumer_groups(id) ON DELETE CASCADE,
 enabled boolean NOT NULL,
 unassigned_threshold bigint NOT NULL CHECK (unassigned_threshold BETWEEN 0 AND 1000000000),
 oldest_pending_seconds integer NOT NULL CHECK (oldest_pending_seconds BETWEEN 0 AND 604800),
 dead_letter_threshold integer NOT NULL CHECK (dead_letter_threshold BETWEEN 0 AND 128),
 cooldown_seconds integer NOT NULL CHECK (cooldown_seconds BETWEEN 30 AND 86400),
 breached boolean NOT NULL DEFAULT false,
 observed_backlog bigint,
 observed_age bigint,
 observed_dead integer,
 observed_at timestamptz,
 last_event_at timestamptz,
 next_check_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK (NOT enabled OR unassigned_threshold>0 OR oldest_pending_seconds>0 OR dead_letter_threshold>0)
);
CREATE INDEX IF NOT EXISTS realtime_group_alert_due ON managed_realtime_consumer_group_alerts(next_check_at,group_id) WHERE enabled;
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.consumer_group.lag_exceeded''' in definition)>0 AND position('''realtime.consumer_group.lag_recovered''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event IN (%L,%L))',substring(definition from 8 for length(definition)-8),'realtime.consumer_group.lag_exceeded','realtime.consumer_group.lag_recovered');
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE managed_realtime_consumer_group_alerts;
-- Retain event vocabulary for queued deliveries.
