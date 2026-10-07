-- filename: 20261007111548438_queue_dead_letter_single_event.sql

-- +goose Up
-- One queue message, one dead-letter event (production-us hunt #5, H5-28).
-- A push binding's private consumer (kind='queue') delivers the queued
-- invocation through a trigger record. On exhaustion the queue poller
-- dead-letters the invocation and the dispatcher also writes
-- trigger_dead_letter, so the unified ledger held two events for one
-- message. The invocation is the replay handle (replaying it re-arms the
-- receipt, 20261001094704872); the consumer's receipt is not captured.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_capture_trigger_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    INSERT INTO dead_letter_events (
        account_id, app_id, source, source_id, origin, trigger_id,
        event_payload, headers, error_kind, error_detail, retry_count,
        first_failed_at, last_failed_at, created_at
    )
    SELECT t.account_id, t.app_id, 'trigger_record', NEW.record_id,
           t.kind, NEW.trigger_id,
           COALESCE(r.payload, '{}'::jsonb),
           COALESCE(r.headers, '{}'::jsonb),
           NEW.reason, COALESCE(NEW.detail, '{}'::jsonb),
           COALESCE(r.attempts, 0), NEW.created_at, NEW.created_at, NEW.created_at
      FROM triggers t
      JOIN trigger_records r ON r.id = NEW.record_id
     WHERE t.id = NEW.trigger_id
       AND NOT (t.kind = 'queue' AND t.source IN ('queue', 'delayed_task'))
    ON CONFLICT (source, source_id) DO UPDATE SET
        origin = EXCLUDED.origin,
        trigger_id = EXCLUDED.trigger_id,
        event_payload = EXCLUDED.event_payload,
        headers = EXCLUDED.headers,
        error_kind = EXCLUDED.error_kind,
        error_detail = EXCLUDED.error_detail,
        retry_count = EXCLUDED.retry_count,
        last_failed_at = EXCLUDED.last_failed_at,
        replayed_at = NULL;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_capture_trigger_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    INSERT INTO dead_letter_events (
        account_id, app_id, source, source_id, origin, trigger_id,
        event_payload, headers, error_kind, error_detail, retry_count,
        first_failed_at, last_failed_at, created_at
    )
    SELECT t.account_id, t.app_id, 'trigger_record', NEW.record_id,
           t.kind, NEW.trigger_id,
           COALESCE(r.payload, '{}'::jsonb),
           COALESCE(r.headers, '{}'::jsonb),
           NEW.reason, COALESCE(NEW.detail, '{}'::jsonb),
           COALESCE(r.attempts, 0), NEW.created_at, NEW.created_at, NEW.created_at
      FROM triggers t
      JOIN trigger_records r ON r.id = NEW.record_id
     WHERE t.id = NEW.trigger_id
    ON CONFLICT (source, source_id) DO UPDATE SET
        origin = EXCLUDED.origin,
        trigger_id = EXCLUDED.trigger_id,
        event_payload = EXCLUDED.event_payload,
        headers = EXCLUDED.headers,
        error_kind = EXCLUDED.error_kind,
        error_detail = EXCLUDED.error_detail,
        retry_count = EXCLUDED.retry_count,
        last_failed_at = EXCLUDED.last_failed_at,
        replayed_at = NULL;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
