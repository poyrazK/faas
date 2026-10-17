-- filename: 20261016170332382_internal_mail_encryption_rotation.sql

-- +goose Up
-- +goose StatementBegin
-- Only identifiers and irreversible retirement markers enter PostgreSQL.
-- Existing ciphertext/HMACs retain their original format until explicit work.
CREATE TABLE IF NOT EXISTS internal_mail_encryption_state (
    namespace text PRIMARY KEY CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    active_key_id text NOT NULL CHECK (active_key_id ~ '^[0-9a-f]{64}$'),
    legacy_key_id text NOT NULL CHECK (legacy_key_id ~ '^[0-9a-f]{64}$'),
    revision bigint NOT NULL CHECK (revision>0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at))
);
CREATE TABLE IF NOT EXISTS internal_mail_encryption_keys (
    namespace text NOT NULL REFERENCES internal_mail_encryption_state(namespace),
    key_id text NOT NULL CHECK (key_id ~ '^[0-9a-f]{64}$'),
    retired_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(retired_at)),
    PRIMARY KEY(namespace,key_id)
);
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_fingerprint_check;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_fingerprint_check CHECK (
    payload_fingerprint IS NULL OR octet_length(payload_fingerprint)=32 OR (
        octet_length(payload_fingerprint)=103
        AND substring(payload_fingerprint from 1 for 6)=convert_to('GMFP2:','UTF8')
        AND encode(substring(payload_fingerprint from 7 for 64),'escape') ~ '^[0-9a-f]{64}$'
        AND substring(payload_fingerprint from 71 for 1)=convert_to(':','UTF8')
    )
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
LOCK TABLE internal_mail_messages, internal_mail_domains, internal_mail_dkim_keys, internal_mail_operations IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
        WHERE table_schema=current_schema() AND table_name='internal_mail_encryption_state') THEN
        LOCK TABLE internal_mail_encryption_state IN ACCESS EXCLUSIVE MODE;
        LOCK TABLE internal_mail_encryption_keys IN ACCESS EXCLUSIVE MODE;
        IF EXISTS (SELECT 1 FROM internal_mail_encryption_state) OR EXISTS (SELECT 1 FROM internal_mail_encryption_keys) THEN
            RAISE EXCEPTION 'internal mail cannot discard encryption selection or retirement state';
        END IF;
    END IF;
    IF EXISTS (SELECT 1 FROM internal_mail_operations WHERE action IN ('encryption_activate','encryption_rewrap','encryption_retire'))
        OR EXISTS (SELECT 1 FROM internal_mail_messages WHERE octet_length(payload_fingerprint)>32
            OR substring(sealed_payload from 1 for 7)=convert_to('GMAIL2:','UTF8'))
        OR EXISTS (SELECT 1 FROM internal_mail_domains WHERE substring(sealed_key from 1 for 7)=convert_to('GMAIL2:','UTF8'))
        OR EXISTS (SELECT 1 FROM internal_mail_dkim_keys WHERE substring(sealed_key from 1 for 7)=convert_to('GMAIL2:','UTF8')) THEN
        RAISE EXCEPTION 'internal mail cannot discard versioned encryption or audit';
    END IF;
END $$;
DROP TABLE IF EXISTS internal_mail_encryption_keys;
DROP TABLE IF EXISTS internal_mail_encryption_state;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_fingerprint_check;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_fingerprint_check
    CHECK (payload_fingerprint IS NULL OR octet_length(payload_fingerprint)=32);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume'));
-- +goose StatementEnd
