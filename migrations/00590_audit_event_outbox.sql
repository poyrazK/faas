-- filename: 00590_audit_event_outbox.sql
-- +goose Up
-- +goose StatementBegin

-- Public-release backend hardening.
--
-- imaged previously handed signature audit events to apid exclusively through
-- pg_notify. LISTEN/NOTIFY is an excellent low-latency wakeup, but it is not a
-- durable queue: a reconnect window could lose the only copy of a verify
-- failure. Keep the notification as the fast path and persist the handoff so
-- apid can claim, retry, and replay it after a restart.
CREATE TABLE IF NOT EXISTS audit_event_outbox (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor        text NOT NULL,
    kind         text NOT NULL,
    subject      uuid,
    data         jsonb NOT NULL,
    dedupe_key   text NOT NULL,
    state        text NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending', 'processing', 'delivered', 'dead_letter')),
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_by   text,
    claimed_at   timestamptz,
    lease_until  timestamptz,
    delivered_at timestamptz,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT audit_event_outbox_dedupe_key_uniq UNIQUE (dedupe_key)
);

CREATE INDEX IF NOT EXISTS audit_event_outbox_claim_idx
    ON audit_event_outbox (state, available_at, id)
    WHERE state IN ('pending', 'processing');

-- The FK is nullable and uses SET NULL so pruning queue metadata never erases
-- the append-only audit evidence that was produced from it.
ALTER TABLE events
    ADD COLUMN IF NOT EXISTS outbox_id bigint
        REFERENCES audit_event_outbox(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS events_outbox_id_uniq
    ON events (outbox_id)
    WHERE outbox_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS events_outbox_id_uniq;
ALTER TABLE events DROP COLUMN IF EXISTS outbox_id;
DROP INDEX IF EXISTS audit_event_outbox_claim_idx;
DROP TABLE IF EXISTS audit_event_outbox;
-- +goose StatementEnd
