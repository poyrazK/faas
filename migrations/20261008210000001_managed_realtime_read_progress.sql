-- +goose Up
CREATE TABLE managed_realtime_read_progress (
 endpoint_id uuid NOT NULL REFERENCES managed_realtime_endpoints(id) ON DELETE CASCADE,
 principal text NOT NULL CHECK(principal ~ '^[0-9a-f]{64}$'),
 stream text NOT NULL CHECK(length(stream) BETWEEN 1 AND 256),
 inbox boolean NOT NULL,
 sequence bigint NOT NULL CHECK(sequence >= 0),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(endpoint_id,principal,stream,inbox),
 CHECK(NOT inbox OR stream = principal)
);
-- +goose StatementBegin
DO $$
DECLARE definition text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO definition FROM pg_constraint
 WHERE conrelid='app_webhook_event_outbox'::regclass AND conname='app_webhook_event_outbox_event_chk';
 IF definition IS NULL THEN RAISE EXCEPTION 'missing webhook event constraint'; END IF;
 definition:=regexp_replace(definition,' NOT VALID$','');
 ALTER TABLE app_webhook_event_outbox DROP CONSTRAINT app_webhook_event_outbox_event_chk;
 EXECUTE format('ALTER TABLE app_webhook_event_outbox ADD CONSTRAINT app_webhook_event_outbox_event_chk CHECK ((%s) OR event=%L)',substring(definition from 8 for length(definition)-8),'realtime.message.read');
END $$;
-- +goose StatementEnd
-- +goose Down
DROP TABLE managed_realtime_read_progress;
-- Preserve the event vocabulary so historical deliveries remain replayable.
