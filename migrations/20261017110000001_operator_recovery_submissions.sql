-- +goose Up
CREATE TABLE operator_recovery_submissions (
    actor_id uuid NOT NULL CHECK (actor_id <> '00000000-0000-0000-0000-000000000000'),
    key_digest text NOT NULL CHECK (key_digest ~ '^[0-9a-f]{64}$'),
    request_digest text NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    intent_id uuid NOT NULL UNIQUE REFERENCES operator_intents(id) ON DELETE RESTRICT,
    response_body bytea NOT NULL CHECK (octet_length(response_body) > 0),
    audit_outbox_id bigint NOT NULL CHECK (audit_outbox_id > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, key_digest)
);
COMMENT ON TABLE operator_recovery_submissions IS
    'ADR-1524: retained acceptance boundary; no TTL. audit_outbox_id survives queue pruning.';

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM operator_recovery_submissions) THEN
        RAISE EXCEPTION 'Preserve operator recovery submission receipts before downgrade';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE operator_recovery_submissions;
