-- filename: 20261016095545574_internal_mail_producers.sql

-- +goose Up
-- +goose StatementBegin
-- Internal producer identity is independent of credential rotation. Legacy
-- service callers retain the empty producer scope and their existing keys.
ALTER TABLE internal_mail_messages ADD COLUMN IF NOT EXISTS producer_id text NOT NULL DEFAULT '';
ALTER TABLE internal_mail_messages ADD COLUMN IF NOT EXISTS sender_address text NOT NULL DEFAULT '';
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_producer_check;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_producer_check
    CHECK ((producer_id='' AND sender_address='')
        OR (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$' AND length(sender_address) BETWEEN 3 AND 254 AND position('@' IN sender_address)>1));
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_namespace_idempotency_key_key;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_producer_idempotency_key;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_producer_idempotency_key
    UNIQUE(namespace,producer_id,idempotency_key);
CREATE INDEX IF NOT EXISTS internal_mail_messages_producer_created_idx ON internal_mail_messages(namespace,producer_id,created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE internal_mail_messages IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema()
        AND table_name='internal_mail_messages' AND column_name='producer_id') THEN
        IF EXISTS (SELECT 1 FROM internal_mail_messages WHERE producer_id<>'') THEN
            RAISE EXCEPTION 'internal mail cannot discard durable producer ownership';
        END IF;
    END IF;
END $$;
DROP INDEX IF EXISTS internal_mail_messages_producer_created_idx;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_producer_idempotency_key;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_namespace_idempotency_key_key;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_namespace_idempotency_key_key UNIQUE(namespace,idempotency_key);
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_producer_check;
ALTER TABLE internal_mail_messages DROP COLUMN IF EXISTS sender_address;
ALTER TABLE internal_mail_messages DROP COLUMN IF EXISTS producer_id;
-- +goose StatementEnd
