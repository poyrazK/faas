-- migration-compat: expand
-- filename: 20261113190512417_async_backlog_bytes.sql

-- ADR-20261022105723 amendment 2026-11-13: async invocations (API, async
-- routes, function send()) and delayed tasks meet the account backlog bytes
-- cap queue sends meet, so their pending payload bytes are counted too.
--
-- invocation_backlog_bytes is applied on insert and again on every
-- decrement, so its answer for a row must not change while the row is
-- counted. Writers are held off while the function changes and the newly
-- counted sources' bytes are recomputed from their pending rows, as the
-- counters' own migration counted them under its trigger lock.

-- +goose Up
SET LOCAL lock_timeout = '5s';
LOCK TABLE invocations IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_backlog_bytes(source text, payload jsonb) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN source IN ('queue', 'async_invoke', 'delayed_task')
        THEN octet_length(payload::text)::bigint ELSE 0 END
$$;
-- +goose StatementEnd

UPDATE invocation_backlog_counters c SET bytes = r.bytes
FROM (
    SELECT account_id, app_id, source, (hashtext(id::text) & 7)::smallint AS slot,
        sum(octet_length(payload::text))::bigint AS bytes
    FROM invocations
    WHERE state IN ('pending', 'dispatching') AND source IN ('async_invoke', 'delayed_task')
    GROUP BY account_id, app_id, source, hashtext(id::text) & 7
) r
WHERE c.account_id = r.account_id AND c.app_id = r.app_id AND c.source = r.source AND c.slot = r.slot;

-- +goose Down
SET LOCAL lock_timeout = '5s';
LOCK TABLE invocations IN SHARE ROW EXCLUSIVE MODE;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION invocation_backlog_bytes(source text, payload jsonb) RETURNS bigint
LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN source = 'queue' THEN octet_length(payload::text)::bigint ELSE 0 END
$$;
-- +goose StatementEnd

UPDATE invocation_backlog_counters SET bytes = 0 WHERE source IN ('async_invoke', 'delayed_task');
