-- migration-compat: expand
-- filename: 20261113190512417_async_backlog_bytes.sql

-- ADR-20261022105723 amendment 2026-11-13: async invocations (API, async
-- routes, function send()) and delayed tasks meet the account backlog bytes
-- cap queue sends meet, so their pending payload bytes are counted too.
--
-- invocation_backlog_bytes is applied on insert and again on every
-- decrement. Async and delayed rows already pending were counted with 0
-- bytes, so their decrement under the new function would take bytes that
-- were never added. Rather than recount every counter under a lock on
-- invocations (an unbatched write, docs/release-compatibility.md), a
-- decrement now stops at zero: those rows' bytes go uncounted until they
-- leave the backlog, and a counter they push to zero is exact again. The
-- error only ever admits more than the cap, never refuses below it, and lasts
-- until the backlog that predates this migration drains.

-- +goose Up
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_backlog_bytes(source text, payload jsonb) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN source IN ('queue', 'async_invoke', 'delayed_task')
        THEN octet_length(payload::text)::bigint ELSE 0 END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A row leaves the backlog when it leaves pending or dispatching, or is
-- deleted, and joins it when it enters either state. Bytes stop at zero (see
-- above).
CREATE OR REPLACE FUNCTION invocation_backlog_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') AND OLD.state IN ('pending', 'dispatching') THEN
        UPDATE invocation_backlog_counters
        SET pending = pending - 1, bytes = greatest(0, bytes - invocation_backlog_bytes(OLD.source, OLD.payload))
        WHERE app_id = OLD.app_id AND source = OLD.source AND account_id = OLD.account_id
            AND slot = (hashtext(OLD.id::text) & 7);
    END IF;
    IF TG_OP IN ('INSERT', 'UPDATE') AND NEW.state IN ('pending', 'dispatching') THEN
        INSERT INTO invocation_backlog_counters AS c (account_id, app_id, source, slot, pending, bytes)
        VALUES (NEW.account_id, NEW.app_id, NEW.source, hashtext(NEW.id::text) & 7, 1,
            invocation_backlog_bytes(NEW.source, NEW.payload))
        ON CONFLICT (app_id, source, account_id, slot)
        DO UPDATE SET pending = c.pending + 1, bytes = c.bytes + excluded.bytes;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_backlog_bytes(source text, payload jsonb) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN source = 'queue' THEN octet_length(payload::text)::bigint ELSE 0 END
$$;
-- +goose StatementEnd

-- The clamp stays: async and delayed bytes counted under Up are cleared
-- below, so a later Up starts them from zero again.
UPDATE invocation_backlog_counters SET bytes = 0 WHERE source IN ('async_invoke', 'delayed_task');
