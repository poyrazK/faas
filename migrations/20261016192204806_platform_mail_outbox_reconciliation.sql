-- filename: 20261016192204806_platform_mail_outbox_reconciliation.sql

-- +goose Up
-- +goose StatementBegin
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 1 CHECK (revision>0);

-- Only apid writes acknowledgement and its audit. Opaque intent/message IDs
-- survive account erasure; no recipient, body, replay key or credential is kept.
CREATE TABLE IF NOT EXISTS platform_mail_outbox_reconciliations (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    request_id uuid NOT NULL CHECK (request_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    intent_id uuid NOT NULL CHECK (intent_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    message_id uuid NOT NULL CHECK (message_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    recovery_id uuid NOT NULL,
    expected_revision bigint NOT NULL CHECK (expected_revision>0),
    resulting_revision bigint NOT NULL CHECK (resulting_revision=expected_revision+1),
    previous_state text NOT NULL CHECK (previous_state IN ('queued','claimed','failed','expired')),
    actor text NOT NULL CHECK (actor ~ '^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,127}$'),
    reason text NOT NULL CHECK (reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    evidence text NOT NULL CHECK (evidence ~ '^[a-zA-Z0-9][a-zA-Z0-9:/._-]{0,255}$'),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY (namespace,request_id),
    UNIQUE(namespace,intent_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_outbox_reconciliations) THEN
        RAISE EXCEPTION 'cannot discard producer mail reconciliation history';
    END IF;
END $$;
DROP TABLE IF EXISTS platform_mail_outbox_reconciliations;
ALTER TABLE platform_mail_producer_outbox DROP COLUMN IF EXISTS revision;
-- +goose StatementEnd
