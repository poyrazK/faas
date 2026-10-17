-- filename: 20261016195608821_platform_mail_verification_outbox.sql

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- apid binds new private-outbox tokens to the address observed at issuance.
-- Empty preserves the existing legacy hash-only token behavior.
ALTER TABLE email_verification_tokens ADD COLUMN IF NOT EXISTS email_address text NOT NULL DEFAULT ''
    CHECK (length(email_address)<=254 AND position(chr(10) in email_address)=0 AND position(chr(13) in email_address)=0);

ALTER TABLE platform_mail_producer_outbox
    ALTER COLUMN deletion_requested_at DROP NOT NULL,
    ALTER COLUMN restore_until DROP NOT NULL,
    ADD COLUMN IF NOT EXISTS verification_token_hash bytea,
    ADD COLUMN IF NOT EXISTS sealed_verification bytea NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS verification_key_id text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS verification_origin text NOT NULL DEFAULT '';
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_code_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_code_check
    CHECK (code IN ('','maild_unavailable','maild_rejected','attempt_limit','notification_expired','account_changed','platform_suppressed','token_unavailable','payload_unavailable'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template='email_verification' AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_verification_keys_idx
    ON platform_mail_producer_outbox(namespace,verification_key_id) WHERE template='email_verification' AND state IN ('queued','claimed');

ALTER TABLE platform_mail_outbox_reconciliations ADD COLUMN IF NOT EXISTS template text NOT NULL DEFAULT 'account_deletion_pending'
    CHECK (template IN ('account_deletion_pending','email_verification'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
LOCK TABLE platform_mail_producer_outbox,platform_mail_outbox_reconciliations,email_verification_tokens IN ACCESS EXCLUSIVE MODE;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM platform_mail_producer_outbox WHERE template='email_verification')
        OR EXISTS (SELECT 1 FROM platform_mail_outbox_reconciliations WHERE template='email_verification')
        OR EXISTS (SELECT 1 FROM email_verification_tokens WHERE email_address<>'') THEN
        RAISE EXCEPTION 'cannot discard verification mail intent, address binding or reconciliation history';
    END IF;
END $$;
ALTER TABLE platform_mail_outbox_reconciliations DROP COLUMN IF EXISTS template;
DROP INDEX IF EXISTS platform_mail_producer_outbox_verification_keys_idx;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check CHECK (template='account_deletion_pending');
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_code_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_code_check
    CHECK (code IN ('','maild_unavailable','maild_rejected','attempt_limit','notification_expired','account_changed','platform_suppressed'));
ALTER TABLE platform_mail_producer_outbox
    ALTER COLUMN deletion_requested_at SET NOT NULL,
    ALTER COLUMN restore_until SET NOT NULL,
    DROP COLUMN IF EXISTS verification_token_hash,
    DROP COLUMN IF EXISTS sealed_verification,
    DROP COLUMN IF EXISTS verification_key_id,
    DROP COLUMN IF EXISTS verification_origin;
ALTER TABLE email_verification_tokens DROP COLUMN IF EXISTS email_address;
-- +goose StatementEnd
