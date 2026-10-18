-- filename: 20261015154854536_internal_mail_operations_audit.sql

-- +goose Up
-- +goose StatementBegin
-- Private operator actions are opt-in. Successful mutations and their audit
-- records commit together; request IDs preserve replay safety after restart.
CREATE TABLE IF NOT EXISTS internal_mail_operations (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    id uuid NOT NULL,
    action text NOT NULL CHECK (action IN ('reconcile','retention_apply')),
    resource text NOT NULL CHECK (length(resource) BETWEEN 1 AND 64),
    actor text NOT NULL CHECK (actor ~ '^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,127}$'),
    reason text NOT NULL CHECK (reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    evidence text NOT NULL CHECK (evidence ~ '^[a-zA-Z0-9][a-zA-Z0-9:/._-]{0,255}$'),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    result jsonb NOT NULL CHECK (jsonb_typeof(result)='object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(namespace,id)
);
CREATE INDEX IF NOT EXISTS internal_mail_operations_resource_idx
    ON internal_mail_operations(namespace,resource,created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Never discard evidence of completed actions during a schema rollback.
DO $$
BEGIN
    IF to_regclass('internal_mail_operations') IS NOT NULL THEN
        LOCK TABLE internal_mail_operations IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_operations) THEN
            RAISE EXCEPTION 'internal mail operations cannot roll back with audit records';
        END IF;
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_operations;
-- +goose StatementEnd
