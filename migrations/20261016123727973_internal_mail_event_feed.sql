-- filename: 20261016123727973_internal_mail_event_feed.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS internal_mail_event_streams (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    last_position bigint NOT NULL CHECK (last_position>0),
    PRIMARY KEY(namespace,producer_id)
);
-- Positions are allocated under the publication lock in the same transaction
-- as the immutable projection. Source event timestamps/IDs are never cursors.
CREATE TABLE IF NOT EXISTS internal_mail_event_feed (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    position bigint NOT NULL CHECK (position>0),
    id uuid NOT NULL,
    source_event_id text NOT NULL,
    message_id uuid NOT NULL,
    recipient_id uuid NOT NULL,
    domain_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled','expired')),
    occurred_at timestamptz NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(published_at)),
    PRIMARY KEY(namespace,producer_id,position),
    UNIQUE(namespace,id),
    UNIQUE(namespace,source_event_id),
    FOREIGN KEY(namespace,producer_id) REFERENCES internal_mail_event_streams(namespace,producer_id),
    FOREIGN KEY(namespace,source_event_id) REFERENCES internal_mail_events(namespace,id),
    FOREIGN KEY(namespace,message_id) REFERENCES internal_mail_messages(namespace,id),
    FOREIGN KEY(namespace,recipient_id) REFERENCES internal_mail_recipients(namespace,id),
    FOREIGN KEY(namespace,domain_id) REFERENCES internal_mail_domains(namespace,id)
);
CREATE TABLE IF NOT EXISTS internal_mail_event_consumers (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_-]{0,31}$'),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
    checkpoint bigint NOT NULL DEFAULT 0 CHECK (checkpoint>=0),
    pending_receipt uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    last_pull_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(last_pull_at)),
    last_ack_at timestamptz CHECK (isfinite(last_ack_at)),
    PRIMARY KEY(namespace,producer_id,name)
);
CREATE TABLE IF NOT EXISTS internal_mail_event_receipts (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    consumer text NOT NULL,
    id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation>0),
    from_position bigint NOT NULL CHECK (from_position>=0),
    to_position bigint NOT NULL CHECK (to_position>from_position),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    acknowledged_at timestamptz CHECK (isfinite(acknowledged_at)),
    PRIMARY KEY(namespace,producer_id,consumer,id),
    FOREIGN KEY(namespace,producer_id,consumer) REFERENCES internal_mail_event_consumers(namespace,producer_id,name),
    FOREIGN KEY(namespace,producer_id,to_position) REFERENCES internal_mail_event_feed(namespace,producer_id,position)
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='internal_mail_event_streams') THEN
        LOCK TABLE internal_mail_event_streams IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_event_streams) THEN
            RAISE EXCEPTION 'internal mail cannot discard durable event stream positions';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='internal_mail_event_feed') THEN
        LOCK TABLE internal_mail_event_feed IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_event_feed) THEN
            RAISE EXCEPTION 'internal mail cannot discard published delivery events';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='internal_mail_event_consumers') THEN
        LOCK TABLE internal_mail_event_consumers IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_event_consumers) THEN
            RAISE EXCEPTION 'internal mail cannot discard saved consumer checkpoints';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name='internal_mail_event_receipts') THEN
        LOCK TABLE internal_mail_event_receipts IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_event_receipts) THEN
            RAISE EXCEPTION 'internal mail cannot discard delivery acknowledgments';
        END IF;
    END IF;
    LOCK TABLE internal_mail_operations IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM internal_mail_operations WHERE action='event_feed_replay') THEN
        RAISE EXCEPTION 'internal mail cannot discard event replay audit';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_event_receipts;
DROP TABLE IF EXISTS internal_mail_event_consumers;
DROP TABLE IF EXISTS internal_mail_event_feed;
DROP TABLE IF EXISTS internal_mail_event_streams;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release'));
-- +goose StatementEnd
