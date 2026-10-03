-- +goose Up
-- +goose StatementBegin
-- Only snapshot_boot references a node-local builder export. Invalid payloads
-- must reach the ordinary handler retry/dead-letter path, not poison claims.
CREATE FUNCTION notification_outbox_target_node(event_channel text, event_payload text)
RETURNS text LANGUAGE plpgsql IMMUTABLE STRICT AS $$
DECLARE
    body jsonb;
BEGIN
    IF event_channel <> 'snapshot_boot' THEN
        RETURN '';
    END IF;
    BEGIN
        body := event_payload::jsonb;
    EXCEPTION WHEN data_exception THEN
        RETURN '';
    END;
    IF jsonb_typeof(body -> 'node_id') IS DISTINCT FROM 'string' THEN
        RETURN '';
    END IF;
    -- Match Go strings.TrimSpace, including Unicode White_Space.
    RETURN btrim(body ->> 'node_id', E' \t\n\013\f\r' ||
        U&'\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000');
END;
$$;

CREATE INDEX notification_outbox_node_claim_idx
    ON notification_outbox (channel, md5(notification_outbox_target_node(channel, payload)), available_at, id)
    WHERE state IN ('pending', 'processing');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS notification_outbox_node_claim_idx;
DROP FUNCTION IF EXISTS notification_outbox_target_node(text, text);
-- +goose StatementEnd
