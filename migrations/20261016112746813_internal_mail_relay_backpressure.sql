-- filename: 20261016112746813_internal_mail_relay_backpressure.sql

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS internal_mail_relay_health (
    namespace text PRIMARY KEY CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    state text NOT NULL DEFAULT 'paused' CHECK (state IN ('paused','recovering','healthy')),
    reason text NOT NULL DEFAULT 'unobserved' CHECK (reason IN ('unobserved','stale','inventory_unavailable','smtp_unavailable','queue_depth','queue_age','recovering','healthy')),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
    observation_id uuid,
    observed_at timestamptz CHECK (isfinite(observed_at)),
    received_at timestamptz CHECK (isfinite(received_at)),
    inventory_ok boolean NOT NULL DEFAULT false,
    smtp_up boolean NOT NULL DEFAULT false,
    queue_messages bigint NOT NULL DEFAULT 0 CHECK (queue_messages>=0),
    oldest_seconds bigint NOT NULL DEFAULT 0 CHECK (oldest_seconds>=0),
    healthy_since timestamptz CHECK (isfinite(healthy_since)),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
    CHECK ((observation_id IS NULL AND observed_at IS NULL AND received_at IS NULL)
        OR (observation_id IS NOT NULL AND observed_at IS NOT NULL AND received_at IS NOT NULL)),
    CHECK ((state='paused' AND healthy_since IS NULL AND reason NOT IN ('recovering','healthy'))
        OR (state='recovering' AND healthy_since IS NOT NULL AND reason='recovering')
        OR (state='healthy' AND healthy_since IS NOT NULL AND reason='healthy'))
);
CREATE TABLE IF NOT EXISTS internal_mail_relay_events (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    state text NOT NULL CHECK (state IN ('paused','recovering','healthy')),
    reason text NOT NULL CHECK (reason IN ('unobserved','stale','inventory_unavailable','smtp_unavailable','queue_depth','queue_age','recovering','healthy')),
    observation_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(namespace,id),
    UNIQUE(namespace,revision)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()
        AND table_name='internal_mail_relay_health') THEN
        LOCK TABLE internal_mail_relay_health IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_relay_health) THEN
            RAISE EXCEPTION 'internal mail cannot discard durable relay health';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema()
        AND table_name='internal_mail_relay_events') THEN
        LOCK TABLE internal_mail_relay_events IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_relay_events) THEN
            RAISE EXCEPTION 'internal mail cannot discard relay backpressure evidence';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_relay_events;
DROP TABLE IF EXISTS internal_mail_relay_health;
-- +goose StatementEnd
