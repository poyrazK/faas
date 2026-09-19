-- Preserve the event-subscription origin in the unified invocation DLQ.
-- Fan-out invocations intentionally keep the ordinary async source so the
-- scheduler can reuse the existing queue path; the event id header provides
-- the durable projection marker.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_capture_invocation_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    IF NEW.state = 'dead_letter' AND OLD.state IS DISTINCT FROM NEW.state THEN
        INSERT INTO dead_letter_events (
            account_id, app_id, source, source_id, origin, event_payload,
            headers, error_kind, error_detail, retry_count,
            first_failed_at, last_failed_at, created_at
        ) VALUES (
            NEW.account_id, NEW.app_id, 'invocation', NEW.id,
            CASE
                WHEN COALESCE(NEW.headers->>'x-gregale-event-id', '') <> ''
                    THEN 'event_subscription'
                ELSE NEW.source
            END,
            COALESCE(NEW.payload, '{}'::jsonb),
            COALESCE(NEW.headers, '{}'::jsonb),
            COALESCE(NULLIF(NEW.outcome, ''), 'dead_letter'),
            jsonb_build_object('last_error', COALESCE(NEW.last_error, '')),
            NEW.attempts,
            COALESCE(NEW.completed_at, NOW()),
            COALESCE(NEW.completed_at, NOW()),
            COALESCE(NEW.completed_at, NOW())
        )
        ON CONFLICT (source, source_id) DO UPDATE SET
            origin = EXCLUDED.origin,
            event_payload = EXCLUDED.event_payload,
            headers = EXCLUDED.headers,
            error_kind = EXCLUDED.error_kind,
            error_detail = EXCLUDED.error_detail,
            retry_count = EXCLUDED.retry_count,
            last_failed_at = EXCLUDED.last_failed_at,
            replayed_at = NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

UPDATE dead_letter_events
   SET origin = 'event_subscription'
 WHERE source = 'invocation'
   AND COALESCE(headers->>'x-gregale-event-id', '') <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION faas_capture_invocation_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    IF NEW.state = 'dead_letter' AND OLD.state IS DISTINCT FROM NEW.state THEN
        INSERT INTO dead_letter_events (
            account_id, app_id, source, source_id, origin, event_payload,
            headers, error_kind, error_detail, retry_count,
            first_failed_at, last_failed_at, created_at
        ) VALUES (
            NEW.account_id, NEW.app_id, 'invocation', NEW.id, NEW.source,
            COALESCE(NEW.payload, '{}'::jsonb),
            COALESCE(NEW.headers, '{}'::jsonb),
            COALESCE(NULLIF(NEW.outcome, ''), 'dead_letter'),
            jsonb_build_object('last_error', COALESCE(NEW.last_error, '')),
            NEW.attempts,
            COALESCE(NEW.completed_at, NOW()),
            COALESCE(NEW.completed_at, NOW()),
            COALESCE(NEW.completed_at, NOW())
        )
        ON CONFLICT (source, source_id) DO UPDATE SET
            origin = EXCLUDED.origin,
            event_payload = EXCLUDED.event_payload,
            headers = EXCLUDED.headers,
            error_kind = EXCLUDED.error_kind,
            error_detail = EXCLUDED.error_detail,
            retry_count = EXCLUDED.retry_count,
            last_failed_at = EXCLUDED.last_failed_at,
            replayed_at = NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

UPDATE dead_letter_events
   SET origin = 'async_invoke'
 WHERE source = 'invocation'
   AND origin = 'event_subscription'
   AND COALESCE(headers->>'x-gregale-event-id', '') <> '';
-- +goose StatementEnd
