-- filename: 20261016130417046_internal_mail_producer_scheduler.sql

-- +goose Up
-- +goose StatementBegin
-- Private scheduling metadata only. No identities or delivery paths are enabled.
CREATE TABLE IF NOT EXISTS internal_mail_producer_dispatch (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id='' OR producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    last_turn bigint NOT NULL DEFAULT 0 CHECK (last_turn>=0),
    PRIMARY KEY(namespace,producer_id)
);
CREATE TABLE IF NOT EXISTS internal_mail_dispatch_reservations (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    lease_id uuid NOT NULL,
    started_at timestamptz NOT NULL CHECK (isfinite(started_at)),
    PRIMARY KEY(namespace,lease_id),
    FOREIGN KEY(namespace,producer_id) REFERENCES internal_mail_producer_dispatch(namespace,producer_id)
);
CREATE INDEX IF NOT EXISTS internal_mail_dispatch_window_idx
    ON internal_mail_dispatch_reservations(namespace,producer_id,started_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name='internal_mail_producer_dispatch') THEN
        LOCK TABLE internal_mail_producer_dispatch IN ACCESS EXCLUSIVE MODE;
        LOCK TABLE internal_mail_dispatch_reservations IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_producer_dispatch)
            OR EXISTS (SELECT 1 FROM internal_mail_dispatch_reservations) THEN
            RAISE EXCEPTION 'internal mail cannot discard durable producer dispatch state';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_dispatch_reservations;
DROP TABLE IF EXISTS internal_mail_producer_dispatch;
-- +goose StatementEnd
