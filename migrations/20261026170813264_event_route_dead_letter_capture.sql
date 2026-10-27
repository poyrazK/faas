-- migration-compat: expand
-- filename: 20261026170813264_event_route_dead_letter_capture.sql
--
-- ADR-20261026163443: when a receipt with routing failures is deleted (the
-- settled-receipt prune, whatever schedd version runs it), each failed
-- recipient enters the unified dead-letter ledger as source 'event_route'.
-- The previous release (20261026163443517) already lists, refuses and skips
-- such rows. Receipts without a failure skip the trigger (WHEN clause). An
-- account that is gone or pending deletion keeps nothing, and a malformed
-- progress entry is skipped with a warning rather than blocking the prune.
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION capture_event_route_dead_letters() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM accounts a WHERE a.id = OLD.account_id AND a.status <> 'deleted_pending') THEN
        RETURN OLD;
    END IF;
    BEGIN
        INSERT INTO dead_letter_events (account_id, app_id, source, source_id, origin, event_payload, headers,
            error_kind, error_detail, retry_count, first_failed_at, last_failed_at)
        SELECT OLD.account_id, app.id, 'event_route', md5(OLD.id::text || '/' || p.key)::uuid,
               CASE WHEN r.recipient ? 'workflow' THEN 'event_workflow' ELSE 'event_subscription' END,
               OLD.payload, '{}'::jsonb,
               COALESCE(NULLIF(p.outcome->>'failure_code', ''), 'unknown'),
               jsonb_strip_nulls(jsonb_build_object(
                   'subscription_id', p.key,
                   'event_source', OLD.source, 'event_id', OLD.event_id, 'event_type', OLD.event_type,
                   'accepted_at', OLD.created_at,
                   'last_error', NULLIF(p.outcome->>'last_error', ''),
                   'retry_stop_reason', NULLIF(p.outcome->>'retry_stop_reason', ''),
                   'retryable', COALESCE(NULLIF(p.outcome->>'retryable', '')::boolean, false))),
               GREATEST(COALESCE(NULLIF(p.outcome->>'attempts', '')::integer, 0), 0),
               f.failed_at, f.failed_at
        FROM jsonb_each(COALESCE(OLD.recipient_progress, '{}'::jsonb)) AS p(key, outcome)
        LEFT JOIN LATERAL (
            SELECT x.recipient FROM jsonb_array_elements(COALESCE(OLD.recipient_snapshot, '[]'::jsonb)) AS x(recipient)
            WHERE x.recipient->>'id' = p.key LIMIT 1
        ) r ON true
        LEFT JOIN apps app ON app.account_id = OLD.account_id
            AND app.id = CASE WHEN r.recipient->>'app_id' ~* '^[0-9a-f]{8}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{4}-?[0-9a-f]{12}$'
                              THEN (r.recipient->>'app_id')::uuid END
        CROSS JOIN LATERAL (SELECT COALESCE(NULLIF(p.outcome->>'updated_at', '')::timestamptz, OLD.created_at) AS failed_at) f
        WHERE p.outcome->>'state' = 'failed'
        ON CONFLICT (source, source_id) DO NOTHING;
    EXCEPTION WHEN others THEN
        RAISE WARNING 'event_route dead-letter capture skipped for receipt %: %', OLD.id, SQLERRM;
    END;
    RETURN OLD;
END $$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER event_fanout_outbox_route_dead_letters
    BEFORE DELETE ON event_fanout_outbox
    FOR EACH ROW WHEN (OLD.last_error IS NOT NULL)
    EXECUTE FUNCTION capture_event_route_dead_letters();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS event_fanout_outbox_route_dead_letters ON event_fanout_outbox;
DROP FUNCTION IF EXISTS capture_event_route_dead_letters();
