-- Minimal projections of platform-owned and maild-owned read dependencies.
-- This is sqlc input, not an independently applied account migration.
CREATE TABLE accounts (id uuid PRIMARY KEY,email text NOT NULL,status text NOT NULL,plan text NOT NULL DEFAULT 'free',last_quota_warning_at timestamptz,deletion_requested_at timestamptz,email_verified_at timestamptz,abuse_hold_at timestamptz,mfa_enrolled_at timestamptz,mfa_recovery_codes_hash bytea[],mfa_secret_encrypted bytea,mfa_required boolean NOT NULL DEFAULT false,past_due_at timestamptz,suspended_reason text);
CREATE TABLE email_verification_tokens(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,consumed_at timestamptz,created_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE account_passwords(account_id uuid PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,hash text NOT NULL,updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE login_tokens(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE TABLE sessions(id uuid PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,revoked_at timestamptz);
CREATE TABLE api_keys(id uuid PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE);
CREATE TABLE mfa_disable_requests(token_hash bytea PRIMARY KEY,account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,requested_at timestamptz NOT NULL,consumed_at timestamptz);
CREATE TABLE orgs(id uuid PRIMARY KEY,personal_org boolean NOT NULL DEFAULT false,personal_owner_account_id uuid,status text NOT NULL DEFAULT 'active',updated_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE billing_identities(account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,provider text NOT NULL,customer_id text NOT NULL,PRIMARY KEY(account_id,provider),UNIQUE(provider,customer_id));
CREATE TABLE mail_suppressions (email text NOT NULL,expires_at timestamptz);
CREATE TABLE internal_mail_recovery_state (namespace text PRIMARY KEY,recovery_id uuid NOT NULL,held boolean NOT NULL);

CREATE TABLE internal_mail_messages (id uuid PRIMARY KEY,namespace text NOT NULL,producer_id text NOT NULL,idempotency_key text NOT NULL,sender_address text NOT NULL,created_at timestamptz NOT NULL,send_at timestamptz,expires_at timestamptz);
CREATE TABLE internal_mail_recipients (id uuid PRIMARY KEY,namespace text NOT NULL,message_id uuid NOT NULL);

CREATE TABLE usage_minutes(account_id uuid NOT NULL,minute timestamptz NOT NULL,mb_seconds bigint NOT NULL);

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

-- Purpose-specific sign-in credentials never enter login_tokens or reset tokens.
-- password_reset_revision is the shared password mutation counter, including
-- writes while every mail flag is off. Verification state is bound separately.
CREATE TABLE IF NOT EXISTS magic_link_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    email_address text NOT NULL CHECK (length(email_address) BETWEEN 3 AND 254 AND email_address=lower(email_address)
        AND position(chr(10) in email_address)=0 AND position(chr(13) in email_address)=0),
    password_revision bigint NOT NULL CHECK (password_revision>0),
    email_verified_at timestamptz CHECK (email_verified_at IS NULL OR isfinite(email_verified_at)),
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL,
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at)),
    consumed_at timestamptz CHECK (consumed_at IS NULL OR isfinite(consumed_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at) AND expires_at>created_at)
);
CREATE INDEX IF NOT EXISTS magic_link_tokens_expiry_idx ON magic_link_tokens(expires_at,token_hash);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_magic_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,verification_key_id)
    WHERE template='magic_link' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link'));

-- The payload holds only the event time and remaining-code count. Recovery
-- codes, their hashes and TOTP secrets never enter notification intent.
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS sealed_security_alert bytea NOT NULL DEFAULT '';
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS security_alert_key_id text NOT NULL DEFAULT '';
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template='mfa_recovery_used' AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template<>'mfa_recovery_used' AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template='mfa_recovery_used' AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_mfa_alert_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='mfa_recovery_used' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used'));

-- MFA enrollment/secret/policy mutations advance this counter even with mail
-- flags off. Recovery-code burns do not change the enrolled factor.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS mfa_revision bigint NOT NULL DEFAULT 1 CHECK (mfa_revision>0);
CREATE TABLE IF NOT EXISTS mfa_disable_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    email_address text NOT NULL CHECK (length(email_address) BETWEEN 3 AND 254 AND email_address=lower(email_address)
        AND position(chr(10) in email_address)=0 AND position(chr(13) in email_address)=0),
    password_revision bigint NOT NULL CHECK (password_revision>0),
    mfa_revision bigint NOT NULL CHECK (mfa_revision>0),
    mfa_enrolled_at timestamptz NOT NULL CHECK (isfinite(mfa_enrolled_at)),
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL,
    requested_at timestamptz NOT NULL CHECK (isfinite(requested_at)),
    ready_at timestamptz NOT NULL CHECK (isfinite(ready_at) AND ready_at=requested_at+interval '24 hours'),
    expires_at timestamptz NOT NULL CHECK (isfinite(expires_at) AND expires_at=ready_at+interval '24 hours'),
    consumed_at timestamptz CHECK (consumed_at IS NULL OR isfinite(consumed_at))
);
CREATE INDEX IF NOT EXISTS mfa_disable_tokens_expiry_idx ON mfa_disable_tokens(expires_at,token_hash);
CREATE INDEX IF NOT EXISTS mfa_disable_tokens_account_idx ON mfa_disable_tokens(account_id) WHERE consumed_at IS NULL;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template='mfa_recovery_used' AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_mfa_disable_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,verification_key_id)
    WHERE template='mfa_disable_request' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request'));

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_mfa_disabled_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='mfa_disabled' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled'));

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_password_changed_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='password_changed' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed'));

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_mfa_enabled_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='mfa_enabled' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled'));

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_payment_failed_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='payment_failed' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed'));
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS payment_past_due_at timestamptz;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_payment_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_payment_check CHECK (
    (template='payment_failed' AND payment_past_due_at IS NOT NULL AND isfinite(payment_past_due_at) AND expires_at>payment_past_due_at)
    OR (template<>'payment_failed' AND payment_past_due_at IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_payment_episode_idx
    ON platform_mail_producer_outbox(account_id,payment_past_due_at) WHERE template='payment_failed';
-- Permanent, minimal application receipts. Pruning them would let a provider
-- replay re-enter dunning after payment recovery. Account erasure cascades.
CREATE TABLE IF NOT EXISTS billing_payment_failure_receipts (
    provider text NOT NULL CHECK (provider IN ('stripe','paddle','polar')),
    event_id text NOT NULL CHECK (length(event_id) BETWEEN 1 AND 256 AND position(chr(10) in event_id)=0 AND position(chr(13) in event_id)=0),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event_type text NOT NULL CHECK (event_type IN ('payment_failed','subscription_past_due')),
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(processed_at)),
    PRIMARY KEY(provider,event_id)
);
CREATE INDEX IF NOT EXISTS billing_payment_failure_receipts_account_idx ON billing_payment_failure_receipts(account_id);

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_account_restored_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='account_restored' AND state IN ('queued','claimed');
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored'));
-- Persisted confirmation bindings must remain invalidated by every account
-- lifecycle writer, including meterd, legacy billing and a flag rollback.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS billing_mail_revision bigint NOT NULL DEFAULT 1 CHECK (billing_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_account_billing_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.status IS DISTINCT FROM OLD.status OR NEW.past_due_at IS DISTINCT FROM OLD.past_due_at
        OR NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at OR NEW.email IS DISTINCT FROM OLD.email THEN
        NEW.billing_mail_revision := OLD.billing_mail_revision+1;
    ELSE
        NEW.billing_mail_revision := OLD.billing_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS accounts_billing_mail_revision_guard ON accounts;
CREATE TRIGGER accounts_billing_mail_revision_guard
    BEFORE UPDATE OF status,past_due_at,deletion_requested_at,email,billing_mail_revision ON accounts
    FOR EACH ROW EXECUTE FUNCTION advance_account_billing_mail_revision();
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS account_restored_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_restored_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_restored_check CHECK (
    (template='account_restored' AND account_restored_revision IS NOT NULL AND account_restored_revision>0)
    OR (template<>'account_restored' AND account_restored_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_restored_revision_idx
    ON platform_mail_producer_outbox(account_id,account_restored_revision) WHERE template='account_restored';
CREATE TABLE IF NOT EXISTS billing_payment_recovery_receipts (
    provider text NOT NULL CHECK (provider IN ('stripe','paddle','polar')),
    event_id text NOT NULL CHECK (length(event_id) BETWEEN 1 AND 256 AND position(chr(10) in event_id)=0 AND position(chr(13) in event_id)=0),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(processed_at)),
    PRIMARY KEY(provider,event_id)
);
CREATE INDEX IF NOT EXISTS billing_payment_recovery_receipts_account_idx ON billing_payment_recovery_receipts(account_id);

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended'));
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS suspension_past_due_at timestamptz;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS account_suspended_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_suspended_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_suspended_check CHECK (
    (template='account_suspended' AND suspension_past_due_at IS NOT NULL AND isfinite(suspension_past_due_at)
        AND account_suspended_revision IS NOT NULL AND account_suspended_revision>0)
    OR (template<>'account_suspended' AND suspension_past_due_at IS NULL AND account_suspended_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_suspended_period_idx
    ON platform_mail_producer_outbox(account_id,suspension_past_due_at) WHERE template='account_suspended';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_account_suspended_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='account_suspended' AND state IN ('queued','claimed');
-- Private billing-command receipt, also retaining meterd's follow-up lease.
-- It carries no recipient, message body or encryption/decryption material.
CREATE TABLE IF NOT EXISTS billing_account_suspensions (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    past_due_at timestamptz NOT NULL CHECK(isfinite(past_due_at)),
    intent_id uuid NOT NULL UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE CASCADE,
    suspended_at timestamptz NOT NULL CHECK(isfinite(suspended_at)),
    billing_revision bigint NOT NULL CHECK(billing_revision>0),
    followup_completed_at timestamptz CHECK(followup_completed_at IS NULL OR isfinite(followup_completed_at)),
    lease_id uuid CHECK(lease_id IS NULL OR lease_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
    PRIMARY KEY(account_id,past_due_at),
    CHECK((lease_id IS NULL)=(lease_until IS NULL)),
    CHECK(followup_completed_at IS NULL OR lease_id IS NOT NULL)
);

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion'));
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS nonpayment_past_due_at timestamptz;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS nonpayment_deletion_revision bigint;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS nonpayment_deletion_at timestamptz;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_nonpayment_deletion_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_nonpayment_deletion_check CHECK (
    (template='nonpayment_deletion' AND nonpayment_past_due_at IS NOT NULL AND isfinite(nonpayment_past_due_at)
        AND nonpayment_deletion_at IS NOT NULL AND isfinite(nonpayment_deletion_at)
        AND nonpayment_deletion_revision IS NOT NULL AND nonpayment_deletion_revision>0)
    OR (template<>'nonpayment_deletion' AND nonpayment_past_due_at IS NULL AND nonpayment_deletion_revision IS NULL AND nonpayment_deletion_at IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_nonpayment_deletion_period_idx
    ON platform_mail_producer_outbox(account_id,nonpayment_past_due_at) WHERE template='nonpayment_deletion';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_nonpayment_deletion_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='nonpayment_deletion' AND state IN ('queued','claimed');
-- Private non-payment deletion-command receipt, also retaining meterd's follow-up lease.
-- It carries no recipient, message body or encryption/decryption material.
CREATE TABLE IF NOT EXISTS billing_account_nonpayment_deletions (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    past_due_at timestamptz NOT NULL CHECK(isfinite(past_due_at)),
    intent_id uuid NOT NULL UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE CASCADE,
    deletion_requested_at timestamptz NOT NULL CHECK(isfinite(deletion_requested_at)),
    billing_revision bigint NOT NULL CHECK(billing_revision>0),
    followup_completed_at timestamptz CHECK(followup_completed_at IS NULL OR isfinite(followup_completed_at)),
    lease_id uuid CHECK(lease_id IS NULL OR lease_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
    PRIMARY KEY(account_id,past_due_at),
    CHECK((lease_id IS NULL)=(lease_until IS NULL)),
    CHECK(followup_completed_at IS NULL OR lease_id IS NOT NULL)
);

ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_template_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning'));
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_verification_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_verification_check CHECK (
    (template='account_deletion_pending' AND deletion_requested_at IS NOT NULL AND restore_until IS NOT NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0 AND verification_key_id='' AND verification_origin='')
    OR (template IN ('email_verification','password_reset','magic_link','mfa_disable_request') AND deletion_requested_at IS NULL AND restore_until IS NULL
        AND ((state IN ('queued','claimed') AND verification_token_hash IS NOT NULL AND octet_length(verification_token_hash)=32
            AND octet_length(sealed_verification) BETWEEN 64 AND 4096 AND verification_key_id ~ '^[0-9a-f]{64}$'
            AND length(verification_origin) BETWEEN 9 AND 2048 AND verification_origin ~ '^https://[^/?#[:space:]]+$')
        OR (state NOT IN ('queued','claimed') AND verification_token_hash IS NULL AND octet_length(sealed_verification)=0
            AND verification_key_id='' AND verification_origin='')))
);
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_security_alert_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_security_alert_check CHECK (
    (template NOT IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')
    OR (template IN ('mfa_recovery_used','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning') AND
        ((state IN ('queued','claimed') AND octet_length(sealed_security_alert) BETWEEN 64 AND 4096 AND security_alert_key_id ~ '^[0-9a-f]{64}$')
        OR (state NOT IN ('queued','claimed') AND octet_length(sealed_security_alert)=0 AND security_alert_key_id='')))
);
ALTER TABLE platform_mail_outbox_reconciliations DROP CONSTRAINT IF EXISTS platform_mail_outbox_reconciliations_template_check;
ALTER TABLE platform_mail_outbox_reconciliations ADD CONSTRAINT platform_mail_outbox_reconciliations_template_check
    CHECK (template IN ('account_deletion_pending','email_verification','password_reset','magic_link','mfa_recovery_used','mfa_disable_request','mfa_disabled','password_changed','mfa_enabled','payment_failed','account_restored','account_suspended','nonpayment_deletion','quota_warning'));
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS quota_mail_revision bigint NOT NULL DEFAULT 1 CHECK(quota_mail_revision>0);
CREATE OR REPLACE FUNCTION advance_account_quota_mail_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.plan IS DISTINCT FROM OLD.plan OR NEW.email IS DISTINCT FROM OLD.email
        OR NEW.status IS DISTINCT FROM OLD.status OR NEW.past_due_at IS DISTINCT FROM OLD.past_due_at
        OR NEW.deletion_requested_at IS DISTINCT FROM OLD.deletion_requested_at
        OR NEW.last_quota_warning_at IS DISTINCT FROM OLD.last_quota_warning_at THEN
        NEW.quota_mail_revision := OLD.quota_mail_revision+1;
    ELSE
        NEW.quota_mail_revision := OLD.quota_mail_revision;
    END IF;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS accounts_quota_mail_revision_guard ON accounts;
CREATE TRIGGER accounts_quota_mail_revision_guard
    BEFORE UPDATE OF plan,email,status,past_due_at,deletion_requested_at,last_quota_warning_at,quota_mail_revision ON accounts
    FOR EACH ROW EXECUTE FUNCTION advance_account_quota_mail_revision();
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS quota_warning_day timestamptz;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS quota_warning_plan text;
ALTER TABLE platform_mail_producer_outbox ADD COLUMN IF NOT EXISTS quota_warning_revision bigint;
ALTER TABLE platform_mail_producer_outbox DROP CONSTRAINT IF EXISTS platform_mail_producer_outbox_quota_warning_check;
ALTER TABLE platform_mail_producer_outbox ADD CONSTRAINT platform_mail_producer_outbox_quota_warning_check CHECK (
    (template='quota_warning' AND quota_warning_day IS NOT NULL AND isfinite(quota_warning_day)
        AND quota_warning_day=(date_trunc('day',quota_warning_day AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
        AND quota_warning_plan IS NOT NULL AND quota_warning_plan IN ('hobby','pro','scale')
        AND quota_warning_revision IS NOT NULL AND quota_warning_revision>0)
    OR (template<>'quota_warning' AND quota_warning_day IS NULL AND quota_warning_plan IS NULL AND quota_warning_revision IS NULL)
);
CREATE UNIQUE INDEX IF NOT EXISTS platform_mail_producer_outbox_quota_warning_day_idx
    ON platform_mail_producer_outbox(account_id,quota_warning_day) WHERE template='quota_warning';
CREATE INDEX IF NOT EXISTS platform_mail_producer_outbox_quota_warning_keys_idx
    ON platform_mail_producer_outbox(namespace,producer_id,security_alert_key_id)
    WHERE template='quota_warning' AND state IN ('queued','claimed');
-- Business snapshot for meterd's notification retry, not a mail body or recipient.
CREATE TABLE IF NOT EXISTS billing_quota_warnings (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    warning_day timestamptz NOT NULL CHECK(isfinite(warning_day) AND warning_day=(date_trunc('day',warning_day AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')),
    intent_id uuid NOT NULL UNIQUE REFERENCES platform_mail_producer_outbox(id) ON DELETE CASCADE,
    observed_at timestamptz NOT NULL CHECK(isfinite(observed_at) AND observed_at>=warning_day AND observed_at<warning_day+interval '24 hours'),
    plan text NOT NULL CHECK(plan IN ('hobby','pro','scale')),
    used_mb_seconds bigint NOT NULL CHECK(used_mb_seconds>=0),
    quota_gb integer NOT NULL CHECK(quota_gb>0),
    quota_revision bigint NOT NULL CHECK(quota_revision>0),
    followup_completed_at timestamptz CHECK(followup_completed_at IS NULL OR isfinite(followup_completed_at)),
    lease_id uuid CHECK(lease_id IS NULL OR lease_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    lease_until timestamptz CHECK(lease_until IS NULL OR isfinite(lease_until)),
    PRIMARY KEY(account_id,warning_day),
    CHECK((lease_id IS NULL)=(lease_until IS NULL)),
    CHECK(followup_completed_at IS NULL OR lease_id IS NOT NULL)
);
