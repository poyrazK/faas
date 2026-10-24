-- migration-compat: expand
-- filename: 20261022112813529_invocation_backlog_counters.sql

-- ADR-20261022105723: a queue may hold up to 1,000,000 pending messages per
-- app, so admission can no longer count an app's pending work on every send,
-- and the per-account queue byte cap cannot sum payloads. Row triggers keep
-- exact pending counts, and for queue rows payload bytes, per account, app
-- and source, split over eight slots by invocation id so concurrent writers
-- rarely share a counter row. The counters cover every pending row, so
-- their sum bounds an app's production backlog from above; only a sum at
-- the cap needs the exact production count.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS invocation_backlog_counters (
    account_id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    source text NOT NULL CHECK (source <> '' AND octet_length(source) <= 64),
    slot smallint NOT NULL CHECK (slot BETWEEN 0 AND 7),
    pending bigint NOT NULL DEFAULT 0,
    bytes bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (app_id, source, account_id, slot)
);
CREATE INDEX IF NOT EXISTS invocation_backlog_counters_account_idx ON invocation_backlog_counters (account_id, source);

-- +goose StatementBegin
-- The bytes a pending queue message holds: its payload as text, the same
-- measure on insert and on every later decrement.
CREATE OR REPLACE FUNCTION invocation_backlog_bytes(source text, payload jsonb) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN source = 'queue' THEN octet_length(payload::text)::bigint ELSE 0 END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A row leaves the backlog when it leaves pending or dispatching, or is
-- deleted, and joins it when it enters either state.
CREATE OR REPLACE FUNCTION invocation_backlog_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP IN ('UPDATE', 'DELETE') AND OLD.state IN ('pending', 'dispatching') THEN
        UPDATE invocation_backlog_counters
        SET pending = pending - 1, bytes = bytes - invocation_backlog_bytes(OLD.source, OLD.payload)
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

CREATE OR REPLACE TRIGGER invocation_backlog_insert AFTER INSERT ON invocations FOR EACH ROW
    WHEN (NEW.state IN ('pending', 'dispatching'))
    EXECUTE FUNCTION invocation_backlog_count();
-- A claim moves pending to dispatching and changes nothing here; the WHEN
-- keeps the drain's claims off the counters.
CREATE OR REPLACE TRIGGER invocation_backlog_state AFTER UPDATE OF state, source, app_id, account_id ON invocations FOR EACH ROW
    WHEN ((OLD.state IN ('pending', 'dispatching')) IS DISTINCT FROM (NEW.state IN ('pending', 'dispatching'))
        OR (OLD.state IN ('pending', 'dispatching') AND (OLD.source IS DISTINCT FROM NEW.source
            OR OLD.app_id IS DISTINCT FROM NEW.app_id OR OLD.account_id IS DISTINCT FROM NEW.account_id)))
    EXECUTE FUNCTION invocation_backlog_count();
-- Payload rewrites of a row that stays in the backlog. Separate from the
-- state trigger so a claim never compares payloads.
CREATE OR REPLACE TRIGGER invocation_backlog_payload AFTER UPDATE OF payload ON invocations FOR EACH ROW
    WHEN (OLD.state IN ('pending', 'dispatching') AND NEW.state IN ('pending', 'dispatching')
        AND OLD.source IS NOT DISTINCT FROM NEW.source AND OLD.app_id IS NOT DISTINCT FROM NEW.app_id
        AND OLD.account_id IS NOT DISTINCT FROM NEW.account_id AND OLD.payload IS DISTINCT FROM NEW.payload)
    EXECUTE FUNCTION invocation_backlog_count();
CREATE OR REPLACE TRIGGER invocation_backlog_delete AFTER DELETE ON invocations FOR EACH ROW
    WHEN (OLD.state IN ('pending', 'dispatching'))
    EXECUTE FUNCTION invocation_backlog_count();

-- Counting after the triggers exist, in their transaction: their lock keeps
-- writers out until commit, so no row is counted twice or missed. The read
-- covers only pending and dispatching rows (invocations_app_pending_idx).
INSERT INTO invocation_backlog_counters (account_id, app_id, source, slot, pending, bytes)
SELECT account_id, app_id, source, hashtext(id::text) & 7, count(*), sum(invocation_backlog_bytes(source, payload))
FROM invocations
WHERE state IN ('pending', 'dispatching')
GROUP BY account_id, app_id, source, hashtext(id::text) & 7;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS invocation_backlog_delete ON invocations;
DROP TRIGGER IF EXISTS invocation_backlog_payload ON invocations;
DROP TRIGGER IF EXISTS invocation_backlog_state ON invocations;
DROP TRIGGER IF EXISTS invocation_backlog_insert ON invocations;
DROP FUNCTION IF EXISTS invocation_backlog_count();
DROP FUNCTION IF EXISTS invocation_backlog_bytes(text, jsonb);
DROP TABLE IF EXISTS invocation_backlog_counters;
