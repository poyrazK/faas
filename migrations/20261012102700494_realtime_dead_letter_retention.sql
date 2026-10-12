-- +goose Up
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS dead_letter_retention_seconds integer NOT NULL DEFAULT 0 CHECK (dead_letter_retention_seconds=0 OR dead_letter_retention_seconds BETWEEN 3600 AND 2592000);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS expired_dead_letters bigint NOT NULL DEFAULT 0 CHECK (expired_dead_letters>=0);
ALTER TABLE managed_realtime_consumer_groups ADD COLUMN IF NOT EXISTS next_retention_check_at timestamptz NOT NULL DEFAULT clock_timestamp();
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_groups') AND conname='realtime_group_retention_ordering') THEN
ALTER TABLE managed_realtime_consumer_groups ADD CONSTRAINT realtime_group_retention_ordering CHECK (dead_letter_retention_seconds=0 OR ordering_metadata_key='' OR dead_letter_policy='skip');
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS dead_since timestamptz;
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS expiry_warned_at timestamptz;
-- Legacy dead snapshots start their retention clock at migration, not a guessed failure time.
UPDATE managed_realtime_consumer_group_messages SET dead_since=clock_timestamp() WHERE status='dead' AND dead_since IS NULL;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_group_messages') AND conname='realtime_dead_retention_state') THEN
ALTER TABLE managed_realtime_consumer_group_messages ADD CONSTRAINT realtime_dead_retention_state CHECK ((status='dead')=(dead_since IS NOT NULL));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_consumer_group_messages') AND conname='realtime_dead_warning_state') THEN
ALTER TABLE managed_realtime_consumer_group_messages ADD CONSTRAINT realtime_dead_warning_state CHECK (expiry_warned_at IS NULL OR status='dead');
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS realtime_group_dead_retention_due ON managed_realtime_consumer_groups(next_retention_check_at,id) WHERE dead_letter_retention_seconds>0;
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF to_regprocedure('maintain_realtime_dead_retention()') IS NULL THEN
CREATE FUNCTION maintain_realtime_dead_retention() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status='dead' AND OLD.status<>'dead' THEN NEW.dead_since:=clock_timestamp(); NEW.expiry_warned_at:=NULL;
 ELSIF NEW.status<>'dead' THEN NEW.dead_since:=NULL; NEW.expiry_warned_at:=NULL;
 END IF;
 RETURN NEW;
END $$;
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass('managed_realtime_consumer_group_messages') AND tgname='realtime_dead_retention_state') THEN
CREATE TRIGGER realtime_dead_retention_state BEFORE UPDATE ON managed_realtime_consumer_group_messages FOR EACH ROW EXECUTE FUNCTION maintain_realtime_dead_retention();
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 IF position('''realtime.consumer_group.dead_letter_expiring''' in definition)>0 THEN RETURN; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'realtime.consumer_group.dead_letter_expiring');
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TRIGGER realtime_dead_retention_state ON managed_realtime_consumer_group_messages;
DROP FUNCTION maintain_realtime_dead_retention();
ALTER TABLE managed_realtime_consumer_group_messages DROP CONSTRAINT realtime_dead_retention_state,DROP CONSTRAINT realtime_dead_warning_state,DROP COLUMN dead_since,DROP COLUMN expiry_warned_at;
DROP INDEX realtime_group_dead_retention_due;
ALTER TABLE managed_realtime_consumer_groups DROP CONSTRAINT realtime_group_retention_ordering,DROP COLUMN dead_letter_retention_seconds,DROP COLUMN expired_dead_letters,DROP COLUMN next_retention_check_at;
-- Retain webhook vocabulary for queued deliveries; deleted snapshots cannot be restored.
