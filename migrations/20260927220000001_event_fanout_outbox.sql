-- +goose Up
-- +goose StatementBegin
-- Every accepted event.published ledger row creates durable fanout work in
-- the same transaction, regardless of which daemon produced it. The unique
-- CloudEvents identity is (account, source, id); retries with the same type
-- and data are harmless, while reuse with different content is rejected.
CREATE TABLE event_fanout_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source text NOT NULL,
    event_id text NOT NULL,
    event_type text NOT NULL,
    schema_version text,
    event_data jsonb NOT NULL,
    payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'processing', 'delivered')),
    attempts integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    claim_token uuid,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    UNIQUE (account_id, source, event_id)
);
CREATE INDEX event_fanout_outbox_pending_idx ON event_fanout_outbox
    (available_at, id) WHERE state = 'pending';
CREATE INDEX event_fanout_outbox_lease_idx ON event_fanout_outbox
    (lease_until, id) WHERE state = 'processing';
CREATE INDEX event_fanout_outbox_retention_idx ON event_fanout_outbox
    (delivered_at, id) WHERE state = 'delivered';

CREATE FUNCTION enqueue_event_fanout() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE existing_type text;
DECLARE existing_data jsonb;
DECLARE existing_schema_version text;
BEGIN
    IF NEW.kind <> 'event.published' THEN
        RETURN NEW;
    END IF;
    IF NEW.subject IS NULL OR NEW.data->>'source' IS NULL OR
       NEW.data->>'id' IS NULL OR NEW.data->>'type' IS NULL OR
       NEW.data->'data' IS NULL THEN
        RAISE EXCEPTION 'event.published requires account, source, id, type and data'
            USING ERRCODE = '23514';
    END IF;
    INSERT INTO event_fanout_outbox
        (account_id, source, event_id, event_type, schema_version, event_data, payload)
    VALUES (NEW.subject, NEW.data->>'source', NEW.data->>'id',
            NEW.data->>'type', NEW.data->>'schemaversion', NEW.data->'data', NEW.data)
    ON CONFLICT (account_id, source, event_id) DO NOTHING;
    IF NOT FOUND THEN
        SELECT event_type, schema_version, event_data INTO existing_type, existing_schema_version, existing_data
        FROM event_fanout_outbox WHERE account_id = NEW.subject
          AND source = NEW.data->>'source' AND event_id = NEW.data->>'id';
        IF existing_type IS DISTINCT FROM NEW.data->>'type' OR
           existing_schema_version IS DISTINCT FROM NEW.data->>'schemaversion' OR
           existing_data IS DISTINCT FROM NEW.data->'data' THEN
            RAISE EXCEPTION 'event identity already has different content'
                USING ERRCODE = '23505', CONSTRAINT = 'event_fanout_identity_uniq';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER events_enqueue_fanout AFTER INSERT ON events
    FOR EACH ROW EXECUTE FUNCTION enqueue_event_fanout();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS events_enqueue_fanout ON events;
DROP FUNCTION IF EXISTS enqueue_event_fanout();
DROP TABLE IF EXISTS event_fanout_outbox;
-- +goose StatementEnd
