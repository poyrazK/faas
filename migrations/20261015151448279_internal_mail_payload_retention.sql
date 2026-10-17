-- filename: 20261015151448279_internal_mail_payload_retention.sql

-- +goose Up
-- +goose StatementBegin
-- Retention is disabled by default. The fingerprint preserves idempotency
-- after payload removal; metadata, delivery evidence and suppressions remain.
ALTER TABLE internal_mail_messages
    ADD COLUMN IF NOT EXISTS payload_fingerprint bytea,
    ADD COLUMN IF NOT EXISTS payload_pruned_at timestamptz;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='internal_mail_messages_fingerprint_check' AND conrelid='internal_mail_messages'::regclass) THEN
        ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_fingerprint_check
            CHECK (payload_fingerprint IS NULL OR octet_length(payload_fingerprint)=32);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='internal_mail_messages_pruned_check' AND conrelid='internal_mail_messages'::regclass) THEN
        ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_pruned_check
            CHECK (payload_pruned_at IS NULL OR (octet_length(sealed_payload)=0 AND payload_fingerprint IS NOT NULL));
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS internal_mail_messages_retention_idx
    ON internal_mail_messages(namespace,created_at,id) WHERE payload_pruned_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Schema rollback cannot recreate removed payloads or discard the only
-- surviving idempotency verifier. Refuse it once cleanup has actually run.
-- Serialize the guard with concurrent cleanup before observing prune markers.
LOCK TABLE internal_mail_messages IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='internal_mail_messages'::regclass AND attname='payload_pruned_at' AND NOT attisdropped) THEN
        IF EXISTS (SELECT 1 FROM internal_mail_messages WHERE payload_pruned_at IS NOT NULL) THEN
            RAISE EXCEPTION 'internal mail retention cannot roll back after payload cleanup';
        END IF;
    END IF;
END $$;
DROP INDEX IF EXISTS internal_mail_messages_retention_idx;
ALTER TABLE internal_mail_messages
    DROP CONSTRAINT IF EXISTS internal_mail_messages_pruned_check,
    DROP CONSTRAINT IF EXISTS internal_mail_messages_fingerprint_check,
    DROP COLUMN IF EXISTS payload_pruned_at,
    DROP COLUMN IF EXISTS payload_fingerprint;
-- +goose StatementEnd
