-- Minimal projections of platform-owned and maild-owned read dependencies.
-- This is sqlc input, not an independently applied account migration.
CREATE TABLE accounts (id uuid PRIMARY KEY,email text NOT NULL,status text NOT NULL,deletion_requested_at timestamptz,email_verified_at timestamptz);
CREATE TABLE email_verification_tokens(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,consumed_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE account_passwords(account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,hash text NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE mail_suppressions (email text NOT NULL,expires_at timestamptz);
CREATE TABLE internal_mail_recovery_state (namespace text PRIMARY KEY,recovery_id uuid NOT NULL,held boolean NOT NULL);

CREATE TABLE internal_mail_messages (id uuid PRIMARY KEY,namespace text NOT NULL,producer_id text NOT NULL,idempotency_key text NOT NULL,sender_address text NOT NULL,created_at timestamptz NOT NULL,send_at timestamptz,expires_at timestamptz);
CREATE TABLE internal_mail_recipients (id uuid PRIMARY KEY,namespace text NOT NULL,message_id uuid NOT NULL);

-- apid owns this durable notification intent and handoff state. maild has
-- metadata-only read access through its existing private operator routes.
CREATE TABLE IF NOT EXISTS platform_mail_producer_outbox (
    id uuid PRIMARY KEY CHECK (id<>'00000000-0000-0000-0000-000000000000'::uuid),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    from_address text NOT NULL CHECK (length(from_address) BETWEEN 3 AND 1024),
    recipient_address text NOT NULL CHECK (length(recipient_address)<=254 AND position(chr(10) in recipient_address)=0 AND position(chr(13) in recipient_address)=0),
    template text NOT NULL CHECK (template='account_deletion_pending'),
    template_version integer NOT NULL CHECK (template_version=1),
    deletion_requested_at timestamptz NOT NULL CHECK (isfinite(deletion_requested_at)),
    restore_until timestamptz NOT NULL CHECK (isfinite(restore_until) AND restore_until>deletion_requested_at),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at>deletion_requested_at),
    recovery_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','claimed','admitted','failed','expired','cancelled','suppressed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts>=0),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(next_attempt_at)),
    lease_id uuid,
    lease_until timestamptz,
    code text NOT NULL DEFAULT '' CHECK (code IN ('','maild_unavailable','maild_rejected','attempt_limit','notification_expired','account_changed','platform_suppressed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    UNIQUE(account_id,template,deletion_requested_at),
    CHECK ((state='claimed')=(lease_id IS NOT NULL AND lease_until IS NOT NULL)),
    CHECK (lease_until IS NULL OR isfinite(lease_until)),
    CHECK ((state IN ('queued','claimed'))=(recipient_address<>''))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_ready_idx
    ON platform_mail_producer_outbox(namespace,producer_id,next_attempt_at,id) WHERE state IN ('queued','claimed');

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

-- Only apid writes passwords. Account-first password writes advance this
-- revision atomically, invalidating every reset issued for an older credential.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS password_reset_revision bigint NOT NULL DEFAULT 1 CHECK (password_reset_revision>0);
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    email_address text NOT NULL CHECK (length(email_address) BETWEEN 3 AND 254 AND email_address=lower(email_address)
        AND position(chr(10) in email_address)=0 AND position(chr(13) in email_address)=0),
    password_revision bigint NOT NULL CHECK (password_revision>0),
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL,
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
    consumed_at timestamptz CHECK (consumed_at IS NULL OR isfinite(consumed_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at) AND expires_at>created_at)
);
CREATE INDEX IF NOT EXISTS password_reset_tokens_expiry_idx ON password_reset_tokens(expires_at,token_hash);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_reset_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,verification_key_id)
    WHERE template='password_reset' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset'));
