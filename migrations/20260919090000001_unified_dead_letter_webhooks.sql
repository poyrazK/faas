-- Extend the unified Failed Events ledger to customer-owned outbound
-- webhook deliveries. The delivery row remains authoritative; this
-- projection gives it the same inspect/replay surface as invocations and
-- broker trigger records.

-- +goose Up
-- +goose StatementBegin

ALTER TABLE dead_letter_events
    DROP CONSTRAINT IF EXISTS dead_letter_events_source_check;

ALTER TABLE dead_letter_events
    ADD CONSTRAINT dead_letter_events_source_check
    CHECK (source IN ('invocation', 'trigger_record', 'webhook_delivery'));

CREATE OR REPLACE FUNCTION faas_capture_app_webhook_dead_letter_event()
RETURNS trigger AS $$
BEGIN
    IF NEW.status = 'dead' AND OLD.status IS DISTINCT FROM NEW.status THEN
        INSERT INTO dead_letter_events (
            account_id, app_id, source, source_id, origin, event_payload,
            headers, error_kind, error_detail, retry_count,
            first_failed_at, last_failed_at, created_at
        ) VALUES (
            NEW.account_id, NEW.app_id, 'webhook_delivery', NEW.id, NEW.event,
            COALESCE(NEW.payload, '{}'::jsonb), '{}'::jsonb,
            CASE WHEN NEW.last_response_code IS NULL
                 THEN 'delivery_failed'
                 ELSE 'http_' || NEW.last_response_code::text END,
            jsonb_build_object(
                'last_error', COALESCE(NEW.last_error, ''),
                'last_response_code', NEW.last_response_code
            ),
            NEW.attempt, COALESCE(NEW.updated_at, NOW()),
            COALESCE(NEW.updated_at, NOW()), COALESCE(NEW.created_at, NOW())
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

DROP TRIGGER IF EXISTS app_webhook_deliveries_capture_dead_letter ON app_webhook_deliveries;
CREATE TRIGGER app_webhook_deliveries_capture_dead_letter
    AFTER UPDATE OF status ON app_webhook_deliveries
    FOR EACH ROW EXECUTE FUNCTION faas_capture_app_webhook_dead_letter_event();

INSERT INTO dead_letter_events (
    account_id, app_id, source, source_id, origin, event_payload, headers,
    error_kind, error_detail, retry_count, first_failed_at, last_failed_at,
    created_at
)
SELECT d.account_id, d.app_id, 'webhook_delivery', d.id, d.event,
       COALESCE(d.payload, '{}'::jsonb), '{}'::jsonb,
       CASE WHEN d.last_response_code IS NULL
            THEN 'delivery_failed'
            ELSE 'http_' || d.last_response_code::text END,
       jsonb_build_object(
           'last_error', COALESCE(d.last_error, ''),
           'last_response_code', d.last_response_code
       ),
       d.attempt, COALESCE(d.updated_at, NOW()), COALESCE(d.updated_at, NOW()),
       COALESCE(d.created_at, NOW())
  FROM app_webhook_deliveries d
 WHERE d.status = 'dead'
ON CONFLICT (source, source_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_webhook_deliveries_capture_dead_letter ON app_webhook_deliveries;
DROP FUNCTION IF EXISTS faas_capture_app_webhook_dead_letter_event();
DELETE FROM dead_letter_events WHERE source = 'webhook_delivery';
ALTER TABLE dead_letter_events
    DROP CONSTRAINT IF EXISTS dead_letter_events_source_check;
ALTER TABLE dead_letter_events
    ADD CONSTRAINT dead_letter_events_source_check
    CHECK (source IN ('invocation', 'trigger_record'));
-- +goose StatementEnd
