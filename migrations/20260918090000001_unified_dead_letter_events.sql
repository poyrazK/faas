-- Unified dead-letter ledger for queue invocations and broker trigger records.
-- The source tables remain authoritative for lifecycle state; this table is
-- the durable, app-scoped history used by the unified DLQ API.

-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS dead_letter_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id          UUID NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    source          TEXT NOT NULL CHECK (source IN ('invocation', 'trigger_record')),
    source_id       UUID NOT NULL,
    origin          TEXT NOT NULL DEFAULT '',
    trigger_id      UUID NULL REFERENCES triggers(id) ON DELETE SET NULL,
    event_payload   JSONB NOT NULL DEFAULT '{}'::jsonb,
    headers         JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_kind      TEXT NOT NULL DEFAULT 'dead_letter',
    error_detail    JSONB NOT NULL DEFAULT '{}'::jsonb,
    retry_count     INT NOT NULL DEFAULT 0 CHECK (retry_count >= 0),
    first_failed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_failed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    replayed_at     TIMESTAMPTZ NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (source, source_id)
);

CREATE INDEX IF NOT EXISTS dead_letter_events_app_created_idx
    ON dead_letter_events (app_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS dead_letter_events_app_open_idx
    ON dead_letter_events (app_id, last_failed_at DESC, id DESC)
    WHERE replayed_at IS NULL;

-- Capture queue/async invocation rows when they enter the terminal
-- dead_letter state. ON CONFLICT also refreshes a row if it is replayed and
-- later exhausts its retry budget again.
DROP TRIGGER IF EXISTS invocations_capture_dead_letter_event ON invocations;
DROP FUNCTION IF EXISTS faas_capture_invocation_dead_letter_event();

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

CREATE TRIGGER invocations_capture_dead_letter_event
    AFTER UPDATE OF state ON invocations
    FOR EACH ROW EXECUTE FUNCTION faas_capture_invocation_dead_letter_event();

-- Capture broker trigger records after the existing trigger_dead_letter row
-- is written. The join preserves the original payload and broker headers.
DROP TRIGGER IF EXISTS trigger_dead_letter_capture_event ON trigger_dead_letter;
DROP FUNCTION IF EXISTS faas_capture_trigger_dead_letter_event();

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

CREATE TRIGGER trigger_dead_letter_capture_event
    AFTER INSERT ON trigger_dead_letter
    FOR EACH ROW EXECUTE FUNCTION faas_capture_trigger_dead_letter_event();

-- Backfill rows that predate the ledger migration. Conflict handling keeps
-- re-applying this migration safe on a partially migrated database.
INSERT INTO dead_letter_events (
    account_id, app_id, source, source_id, origin, event_payload, headers,
    error_kind, error_detail, retry_count, first_failed_at, last_failed_at,
    created_at
)
SELECT i.account_id, i.app_id, 'invocation', i.id, i.source,
       COALESCE(i.payload, '{}'::jsonb), COALESCE(i.headers, '{}'::jsonb),
       COALESCE(NULLIF(i.outcome, ''), 'dead_letter'),
       jsonb_build_object('last_error', COALESCE(i.last_error, '')),
       i.attempts, COALESCE(i.completed_at, i.created_at),
       COALESCE(i.completed_at, i.created_at), i.created_at
  FROM invocations i
 WHERE i.state = 'dead_letter'
ON CONFLICT (source, source_id) DO NOTHING;

INSERT INTO dead_letter_events (
    account_id, app_id, source, source_id, origin, trigger_id,
    event_payload, headers, error_kind, error_detail, retry_count,
    first_failed_at, last_failed_at, created_at
)
SELECT t.account_id, t.app_id, 'trigger_record', d.record_id, t.kind, d.trigger_id,
       COALESCE(r.payload, '{}'::jsonb), COALESCE(r.headers, '{}'::jsonb),
       d.reason, COALESCE(d.detail, '{}'::jsonb), COALESCE(r.attempts, 0),
       d.created_at, d.created_at, d.created_at
  FROM trigger_dead_letter d
  JOIN trigger_records r ON r.id = d.record_id
  JOIN triggers t ON t.id = d.trigger_id
ON CONFLICT (source, source_id) DO NOTHING;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS trigger_dead_letter_capture_event ON trigger_dead_letter;
DROP FUNCTION IF EXISTS faas_capture_trigger_dead_letter_event();
DROP TRIGGER IF EXISTS invocations_capture_dead_letter_event ON invocations;
DROP FUNCTION IF EXISTS faas_capture_invocation_dead_letter_event();
DROP INDEX IF EXISTS dead_letter_events_app_open_idx;
DROP INDEX IF EXISTS dead_letter_events_app_created_idx;
DROP TABLE IF EXISTS dead_letter_events;
-- +goose StatementEnd
