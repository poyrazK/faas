-- filename: 20261015121450648_internal_mail_service.sql

-- +goose Up
-- +goose StatementBegin
-- Internal-only state; no customer API, plan entitlement or runtime flag is
-- enabled by this additive migration. Per-recipient rows are the transactional
-- outbox, committed with their encrypted message payload.
CREATE TABLE IF NOT EXISTS internal_mail_domains (
    id uuid PRIMARY KEY,
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    name text NOT NULL CHECK (name = lower(name) AND length(name) BETWEEN 3 AND 253),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','disabled')),
    selector text NOT NULL,
    verification_token text NOT NULL,
    public_key text NOT NULL,
    sealed_key bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (namespace, name), UNIQUE (namespace, id)
);
CREATE TABLE IF NOT EXISTS internal_mail_messages (
    id uuid PRIMARY KEY,
    namespace text NOT NULL,
    domain_id uuid NOT NULL,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 128),
    sealed_payload bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (namespace, domain_id) REFERENCES internal_mail_domains(namespace, id),
    UNIQUE (namespace, idempotency_key), UNIQUE (namespace, id)
);
CREATE TABLE IF NOT EXISTS internal_mail_recipients (
    id uuid PRIMARY KEY,
    namespace text NOT NULL,
    message_id uuid NOT NULL,
    address text NOT NULL,
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_id uuid,
    lease_until timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    code text NOT NULL DEFAULT '' CHECK (length(code) <= 512),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (namespace, message_id) REFERENCES internal_mail_messages(namespace, id) ON DELETE CASCADE,
    UNIQUE (namespace, id), UNIQUE (message_id, address),
    CHECK ((status IN ('claimed','submitting')) = (lease_id IS NOT NULL AND lease_until IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS internal_mail_recipients_ready_idx ON internal_mail_recipients(namespace,next_attempt_at) WHERE status IN ('queued','deferred','claimed');
CREATE INDEX IF NOT EXISTS internal_mail_messages_created_idx ON internal_mail_messages(namespace,created_at);
CREATE TABLE IF NOT EXISTS internal_mail_events (
    id text NOT NULL CHECK (length(id) BETWEEN 1 AND 128),
    namespace text NOT NULL,
    recipient_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry')),
    code text NOT NULL DEFAULT '' CHECK (length(code) <= 512),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (namespace,id),
    FOREIGN KEY (namespace,recipient_id) REFERENCES internal_mail_recipients(namespace,id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS internal_mail_events_recipient_idx ON internal_mail_events(namespace,recipient_id,created_at);
CREATE TABLE IF NOT EXISTS internal_mail_suppressions (
    namespace text NOT NULL,
    address text NOT NULL CHECK (address = lower(address)),
    reason text NOT NULL CHECK (reason IN ('bounced','complained','manual')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(namespace,address)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS internal_mail_suppressions;
DROP TABLE IF EXISTS internal_mail_events;
DROP TABLE IF EXISTS internal_mail_recipients;
DROP TABLE IF EXISTS internal_mail_messages;
DROP TABLE IF EXISTS internal_mail_domains;
-- +goose StatementEnd
