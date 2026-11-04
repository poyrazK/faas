-- migration-compat: expand
-- filename: 20261023221612418_event_receipt_usage.sql

-- ADR-20261023215834: a Scale account may retain 8,388,608 customer events,
-- so publish admission can no longer count the account's retained receipts
-- on every publish while it holds the account's admission lock. Triggers
-- keep exact per-owner counts and charged bytes of customer receipts
-- (customer_storage_bytes > 0), and every owner with any receipt has a row,
-- which the settled-receipt prune walks to find owners with receipts past
-- their plan's window.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS event_receipt_usage (
    account_id uuid PRIMARY KEY REFERENCES resource_namespaces(id) ON DELETE CASCADE,
    retained_events bigint NOT NULL DEFAULT 0,
    retained_bytes bigint NOT NULL DEFAULT 0
);

-- +goose StatementBegin
-- A customer receipt joins its owner's count on insert, and its charge
-- follows later snapshot rewrites. The ordering snapshot trigger rewrites a
-- new receipt before this insert trigger runs, so a rewrite may create the
-- owner's row. A platform receipt only registers its owner, without locking
-- the counter row customer publishes update.
CREATE OR REPLACE FUNCTION event_receipt_usage_count() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        INSERT INTO event_receipt_usage AS u (account_id, retained_bytes)
        VALUES (NEW.account_id, NEW.customer_storage_bytes - OLD.customer_storage_bytes)
        ON CONFLICT (account_id) DO UPDATE SET retained_bytes = u.retained_bytes + excluded.retained_bytes;
    ELSIF NEW.customer_storage_bytes > 0 THEN
        INSERT INTO event_receipt_usage AS u (account_id, retained_events, retained_bytes)
        VALUES (NEW.account_id, 1, NEW.customer_storage_bytes)
        ON CONFLICT (account_id)
        DO UPDATE SET retained_events = u.retained_events + 1, retained_bytes = u.retained_bytes + excluded.retained_bytes;
    ELSE
        INSERT INTO event_receipt_usage (account_id) VALUES (NEW.account_id) ON CONFLICT DO NOTHING;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- One counter update per owner for each deleting statement, so a prune
-- batch does not rewrite the owner's row once per receipt.
CREATE OR REPLACE FUNCTION event_receipt_usage_release() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    UPDATE event_receipt_usage u
    SET retained_events = u.retained_events - d.events, retained_bytes = u.retained_bytes - d.bytes
    FROM (
        SELECT account_id, count(*) AS events, sum(customer_storage_bytes) AS bytes
        FROM released_receipts WHERE customer_storage_bytes > 0 GROUP BY account_id
    ) d
    WHERE u.account_id = d.account_id;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd

CREATE OR REPLACE TRIGGER event_receipt_usage_insert AFTER INSERT ON event_fanout_outbox FOR EACH ROW
    EXECUTE FUNCTION event_receipt_usage_count();
CREATE OR REPLACE TRIGGER event_receipt_usage_charge AFTER UPDATE OF payload, event_data, recipient_snapshot ON event_fanout_outbox
    FOR EACH ROW WHEN (OLD.customer_storage_bytes IS DISTINCT FROM NEW.customer_storage_bytes)
    EXECUTE FUNCTION event_receipt_usage_count();
CREATE OR REPLACE TRIGGER event_receipt_usage_delete AFTER DELETE ON event_fanout_outbox
    REFERENCING OLD TABLE AS released_receipts FOR EACH STATEMENT
    EXECUTE FUNCTION event_receipt_usage_release();

-- Counting after the triggers exist, in their transaction: their lock keeps
-- writers out until commit, so no receipt is counted twice or missed.
INSERT INTO event_receipt_usage (account_id, retained_events, retained_bytes)
SELECT account_id, count(*) FILTER (WHERE customer_storage_bytes > 0), coalesce(sum(customer_storage_bytes), 0)
FROM event_fanout_outbox
GROUP BY account_id;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS event_receipt_usage_delete ON event_fanout_outbox;
DROP TRIGGER IF EXISTS event_receipt_usage_charge ON event_fanout_outbox;
DROP TRIGGER IF EXISTS event_receipt_usage_insert ON event_fanout_outbox;
DROP FUNCTION IF EXISTS event_receipt_usage_release();
DROP FUNCTION IF EXISTS event_receipt_usage_count();
DROP TABLE IF EXISTS event_receipt_usage;
