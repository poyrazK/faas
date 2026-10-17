-- Projection of the internal mail service and payload-retention migrations for sqlc.
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

ALTER TABLE internal_mail_domains
    ADD COLUMN IF NOT EXISTS dkim_key_id uuid,
    ADD COLUMN IF NOT EXISTS dns_revision bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS authorization_state text NOT NULL DEFAULT 'unchecked',
    ADD COLUMN IF NOT EXISTS last_checked_at timestamptz,
    ADD COLUMN IF NOT EXISTS last_verified_at timestamptz,
    ADD COLUMN IF NOT EXISTS verified_sending_ip text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS check_result text NOT NULL DEFAULT 'unchecked',
    ADD COLUMN IF NOT EXISTS failure_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS first_failure_at timestamptz;
ALTER TABLE internal_mail_recipients ADD COLUMN IF NOT EXISTS signing_selector text NOT NULL DEFAULT '';
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='internal_mail_domains_authorization_check' AND conrelid='internal_mail_domains'::regclass) THEN
        ALTER TABLE internal_mail_domains ADD CONSTRAINT internal_mail_domains_authorization_check
            CHECK (authorization_state IN ('unchecked','valid','failing','paused') AND check_result IN ('unchecked','valid','mismatch','unavailable') AND failure_count BETWEEN 0 AND 2 AND dns_revision>=0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='internal_mail_recipients_selector_check' AND conrelid='internal_mail_recipients'::regclass) THEN
        ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_selector_check CHECK (length(signing_selector)<=63);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS internal_mail_dkim_keys (
    id uuid NOT NULL,
    namespace text NOT NULL,
    domain_id uuid NOT NULL,
    selector text NOT NULL CHECK (selector ~ '^[a-z0-9]{1,63}$'),
    public_key text NOT NULL,
    sealed_key bytea NOT NULL,
    legacy boolean NOT NULL DEFAULT false,
    state text NOT NULL CHECK (state IN ('staged','active','retiring','retired')),
    check_result text NOT NULL DEFAULT 'unchecked' CHECK (check_result IN ('unchecked','valid','mismatch','unavailable')),
    checked_at timestamptz,
    first_verified_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    activated_at timestamptz,
    deactivated_at timestamptz,
    PRIMARY KEY(namespace,id),
    UNIQUE(namespace,domain_id,selector),
    FOREIGN KEY(namespace,domain_id) REFERENCES internal_mail_domains(namespace,id),
    CHECK (NOT legacy OR id=domain_id),
    CHECK (state NOT IN ('retiring','retired') OR deactivated_at IS NOT NULL),
    CHECK (state<>'retired' OR octet_length(sealed_key)=0)
);
CREATE UNIQUE INDEX IF NOT EXISTS internal_mail_dkim_keys_active_idx ON internal_mail_dkim_keys(namespace,domain_id) WHERE state='active';
CREATE UNIQUE INDEX IF NOT EXISTS internal_mail_dkim_keys_staged_idx ON internal_mail_dkim_keys(namespace,domain_id) WHERE state='staged';
INSERT INTO internal_mail_dkim_keys(id,namespace,domain_id,selector,public_key,sealed_key,legacy,state,created_at,activated_at)
SELECT id,namespace,id,selector,public_key,sealed_key,true,'active',created_at,created_at FROM internal_mail_domains
WHERE dkim_key_id IS NULL ON CONFLICT DO NOTHING;

ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify'));

CREATE TABLE IF NOT EXISTS internal_mail_delivery_controls (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    scope_id uuid NOT NULL,
    domain_id uuid,
    mode text NOT NULL CHECK (mode IN ('running','paused','draining')),
    revision bigint NOT NULL CHECK (revision>0),
    request_id uuid NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(namespace,scope_id),
    FOREIGN KEY(namespace,domain_id) REFERENCES internal_mail_domains(namespace,id),
    CHECK ((domain_id IS NULL AND scope_id='00000000-0000-0000-0000-000000000000'::uuid)
        OR (domain_id IS NOT NULL AND scope_id=domain_id AND scope_id<>'00000000-0000-0000-0000-000000000000'::uuid))
);
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_status_check;
ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_status_check
    CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','cancelled'));
ALTER TABLE internal_mail_events DROP CONSTRAINT IF EXISTS internal_mail_events_kind_check;
ALTER TABLE internal_mail_events ADD CONSTRAINT internal_mail_events_kind_check
    CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled'));
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel'));

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

-- Timing is durable metadata. No feature gate or delivery path is activated.
ALTER TABLE internal_mail_messages ADD COLUMN IF NOT EXISTS send_at timestamptz;
ALTER TABLE internal_mail_messages ADD COLUMN IF NOT EXISTS expires_at timestamptz;
ALTER TABLE internal_mail_messages DROP CONSTRAINT IF EXISTS internal_mail_messages_timing_check;
ALTER TABLE internal_mail_messages ADD CONSTRAINT internal_mail_messages_timing_check
    CHECK ((send_at IS NULL OR (isfinite(send_at) AND send_at>=created_at))
       AND (expires_at IS NULL OR (isfinite(expires_at) AND expires_at>coalesce(send_at,created_at))));
ALTER TABLE internal_mail_recipients DROP CONSTRAINT IF EXISTS internal_mail_recipients_status_check;
ALTER TABLE internal_mail_recipients ADD CONSTRAINT internal_mail_recipients_status_check
    CHECK (status IN ('queued','claimed','submitting','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','cancelled','expired'));
ALTER TABLE internal_mail_events DROP CONSTRAINT IF EXISTS internal_mail_events_kind_check;
ALTER TABLE internal_mail_events ADD CONSTRAINT internal_mail_events_kind_check
    CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled','expired'));

-- A single durable cooldown per namespace and exact recipient domain. This
-- creates no settings, identities or enabled delivery paths.
CREATE TABLE IF NOT EXISTS internal_mail_domain_pacing (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recipient_domain text NOT NULL CHECK (recipient_domain=lower(recipient_domain)
        AND length(recipient_domain) BETWEEN 1 AND 253
        AND recipient_domain ~ '^[a-z0-9][a-z0-9.-]*[a-z0-9]$'),
    next_allowed_at timestamptz NOT NULL CHECK (isfinite(next_allowed_at)),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,recipient_domain)
);

CREATE TABLE IF NOT EXISTS internal_mail_relay_health (
    namespace text PRIMARY KEY CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    state text NOT NULL DEFAULT 'paused' CHECK (state IN ('paused','recovering','healthy')),
    reason text NOT NULL DEFAULT 'unobserved' CHECK (reason IN ('unobserved','stale','inventory_unavailable','smtp_unavailable','queue_depth','queue_age','recovering','healthy')),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
    observation_id uuid,
    observed_at timestamptz CHECK (isfinite(observed_at)),
    received_at timestamptz CHECK (isfinite(received_at)),
    inventory_ok boolean NOT NULL DEFAULT false,
    smtp_up boolean NOT NULL DEFAULT false,
    queue_messages bigint NOT NULL DEFAULT 0 CHECK (queue_messages>=0),
    oldest_seconds bigint NOT NULL DEFAULT 0 CHECK (oldest_seconds>=0),
    healthy_since timestamptz CHECK (isfinite(healthy_since)),
    updated_at timestamptz NOT NULL DEFAULT now() CHECK (isfinite(updated_at)),
    CHECK ((observation_id IS NULL AND observed_at IS NULL AND received_at IS NULL)
        OR (observation_id IS NOT NULL AND observed_at IS NOT NULL AND received_at IS NOT NULL)),
    CHECK ((state='paused' AND healthy_since IS NULL AND reason NOT IN ('recovering','healthy'))
        OR (state='recovering' AND healthy_since IS NOT NULL AND reason='recovering')
        OR (state='healthy' AND healthy_since IS NOT NULL AND reason='healthy'))
);
CREATE TABLE IF NOT EXISTS internal_mail_relay_events (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    state text NOT NULL CHECK (state IN ('paused','recovering','healthy')),
    reason text NOT NULL CHECK (reason IN ('unobserved','stale','inventory_unavailable','smtp_unavailable','queue_depth','queue_age','recovering','healthy')),
    observation_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(namespace,id),
    UNIQUE(namespace,revision)
);

-- Only the authenticated provider ingestion path writes this ledger. First
-- observations never move on replay, even across provider report identities.
CREATE TABLE IF NOT EXISTS internal_mail_reputation_feedback (
    namespace text NOT NULL,
    recipient_id uuid NOT NULL,
    bounced_at timestamptz CHECK (isfinite(bounced_at)),
    complained_at timestamptz CHECK (isfinite(complained_at)),
    bounce_event_id text,
    complaint_event_id text,
    PRIMARY KEY(namespace,recipient_id),
    FOREIGN KEY(namespace,recipient_id) REFERENCES internal_mail_recipients(namespace,id),
    FOREIGN KEY(namespace,bounce_event_id) REFERENCES internal_mail_events(namespace,id),
    FOREIGN KEY(namespace,complaint_event_id) REFERENCES internal_mail_events(namespace,id),
    CHECK ((bounced_at IS NULL)=(bounce_event_id IS NULL)),
    CHECK ((complained_at IS NULL)=(complaint_event_id IS NULL)),
    CHECK (bounced_at IS NOT NULL OR complained_at IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS internal_mail_reputation_feedback_window_idx
    ON internal_mail_reputation_feedback(namespace,(coalesce(complained_at,bounced_at)));
CREATE INDEX IF NOT EXISTS internal_mail_reputation_exposures_idx ON internal_mail_events(namespace,created_at,recipient_id)
    WHERE kind IN ('submitted','uncertain','accepted','bounced','complained');
CREATE TABLE IF NOT EXISTS internal_mail_reputation_holds (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    scope text NOT NULL CHECK (scope IN ('producer','domain')),
    scope_key text NOT NULL,
    held boolean NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    reason text NOT NULL CHECK (reason IN ('bounce_rate','complaint_rate')),
    samples bigint NOT NULL CHECK (samples>0),
    bounces bigint NOT NULL CHECK (bounces>=0),
    complaints bigint NOT NULL CHECK (complaints>=0),
    window_start timestamptz NOT NULL CHECK (isfinite(window_start)),
    window_end timestamptz NOT NULL CHECK (isfinite(window_end) AND window_end>window_start),
    trigger_recipient_id uuid NOT NULL,
    trigger_event_id text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,scope,scope_key),
    CHECK (bounces+complaints<=samples),
    CHECK ((scope='producer' AND (scope_key='' OR scope_key ~ '^[a-z][a-z0-9_-]{0,63}$'))
        OR (scope='domain' AND scope_key ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'))
);
CREATE TABLE IF NOT EXISTS internal_mail_reputation_events (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    id uuid NOT NULL,
    sequence bigint GENERATED ALWAYS AS IDENTITY,
    scope text NOT NULL CHECK (scope IN ('producer','domain')),
    scope_key text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    held boolean NOT NULL,
    reason text NOT NULL CHECK (reason IN ('bounce_rate','complaint_rate')),
    evidence jsonb NOT NULL CHECK (jsonb_typeof(evidence)='object'),
    request_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(namespace,id),
    UNIQUE(namespace,sequence),
    UNIQUE(namespace,scope,scope_key,revision)
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release'));

CREATE TABLE IF NOT EXISTS internal_mail_event_streams (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    last_position bigint NOT NULL CHECK (last_position>0),
    PRIMARY KEY(namespace,producer_id)
);
-- Positions are allocated under the publication lock in the same transaction
-- as the immutable projection. Source event timestamps/IDs are never cursors.
CREATE TABLE IF NOT EXISTS internal_mail_event_feed (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    position bigint NOT NULL CHECK (position>0),
    id uuid NOT NULL,
    source_event_id text NOT NULL,
    message_id uuid NOT NULL,
    recipient_id uuid NOT NULL,
    domain_id uuid NOT NULL,
    kind text NOT NULL CHECK (kind IN ('queued','submitted','accepted','bounced','complained','suppressed','failed','uncertain','deferred','retry','cancelled','expired')),
    occurred_at timestamptz NOT NULL,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(published_at)),
    PRIMARY KEY(namespace,producer_id,position),
    UNIQUE(namespace,id),
    UNIQUE(namespace,source_event_id),
    FOREIGN KEY(namespace,producer_id) REFERENCES internal_mail_event_streams(namespace,producer_id),
    FOREIGN KEY(namespace,source_event_id) REFERENCES internal_mail_events(namespace,id),
    FOREIGN KEY(namespace,message_id) REFERENCES internal_mail_messages(namespace,id),
    FOREIGN KEY(namespace,recipient_id) REFERENCES internal_mail_recipients(namespace,id),
    FOREIGN KEY(namespace,domain_id) REFERENCES internal_mail_domains(namespace,id)
);
CREATE TABLE IF NOT EXISTS internal_mail_event_consumers (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_-]{0,31}$'),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation>0),
    checkpoint bigint NOT NULL DEFAULT 0 CHECK (checkpoint>=0),
    pending_receipt uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    last_pull_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(last_pull_at)),
    last_ack_at timestamptz CHECK (isfinite(last_ack_at)),
    PRIMARY KEY(namespace,producer_id,name)
);
CREATE TABLE IF NOT EXISTS internal_mail_event_receipts (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    consumer text NOT NULL,
    id uuid NOT NULL,
    generation bigint NOT NULL CHECK (generation>0),
    from_position bigint NOT NULL CHECK (from_position>=0),
    to_position bigint NOT NULL CHECK (to_position>from_position),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    acknowledged_at timestamptz CHECK (isfinite(acknowledged_at)),
    PRIMARY KEY(namespace,producer_id,consumer,id),
    FOREIGN KEY(namespace,producer_id,consumer) REFERENCES internal_mail_event_consumers(namespace,producer_id,name),
    FOREIGN KEY(namespace,producer_id,to_position) REFERENCES internal_mail_event_feed(namespace,producer_id,position)
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay'));

-- Private scheduling metadata only. No identities or delivery paths are enabled.
CREATE TABLE IF NOT EXISTS internal_mail_producer_dispatch (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id='' OR producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    last_turn bigint NOT NULL DEFAULT 0 CHECK (last_turn>=0),
    PRIMARY KEY(namespace,producer_id)
);
CREATE TABLE IF NOT EXISTS internal_mail_dispatch_reservations (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    lease_id uuid NOT NULL,
    started_at timestamptz NOT NULL CHECK (isfinite(started_at)),
    PRIMARY KEY(namespace,lease_id),
    FOREIGN KEY(namespace,producer_id) REFERENCES internal_mail_producer_dispatch(namespace,producer_id)
);
CREATE INDEX IF NOT EXISTS internal_mail_dispatch_window_idx
    ON internal_mail_dispatch_reservations(namespace,producer_id,started_at);

-- Credential identifiers are one-way, producer-bound references. Bearer
-- secrets remain in private configuration, never in these tables or audits.
CREATE TABLE IF NOT EXISTS internal_mail_producer_access (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    producer_id text NOT NULL CHECK (producer_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
    suspended boolean NOT NULL DEFAULT false,
    active_credential_id text NOT NULL DEFAULT '' CHECK (active_credential_id='' OR active_credential_id ~ '^[0-9a-f]{64}$'),
    request_id uuid,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,producer_id)
);
CREATE TABLE IF NOT EXISTS internal_mail_producer_credentials (
    namespace text NOT NULL,
    producer_id text NOT NULL,
    credential_id text NOT NULL CHECK (credential_id ~ '^[0-9a-f]{64}$'),
    retires_at timestamptz CHECK (isfinite(retires_at)),
    revoked_at timestamptz CHECK (isfinite(revoked_at)),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY(namespace,producer_id,credential_id),
    FOREIGN KEY(namespace,producer_id) REFERENCES internal_mail_producer_access(namespace,producer_id)
);
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume'));

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

-- The existing table remains the active-block projection. Released addresses
-- retain their revision/history here; no release can revive an older recipient.
CREATE TABLE IF NOT EXISTS internal_mail_suppression_state (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    address text NOT NULL CHECK (address=lower(address) AND length(address) BETWEEN 3 AND 254),
    revision bigint NOT NULL CHECK (revision>0),
    last_block_revision bigint NOT NULL CHECK (last_block_revision>0 AND last_block_revision<=revision),
    blocked boolean NOT NULL,
    reason text NOT NULL CHECK (reason IN ('manual','bounced','complained')),
    cause text NOT NULL CHECK (cause IN ('legacy','manual_add','release','hard_bounce','complaint')),
    source_kind text NOT NULL CHECK (source_kind IN ('legacy','operator','service_report','postfix','provider')),
    source_id text NOT NULL DEFAULT '' CHECK (source_id='' OR source_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    recipient_id uuid,
    event_ref text NOT NULL DEFAULT '' CHECK (event_ref='' OR event_ref ~ '^[0-9a-f]{64}$'),
    request_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    PRIMARY KEY(namespace,address),
    CHECK (reason<>'complained' OR blocked),
    CHECK ((cause='release')=NOT blocked),
    CHECK (NOT blocked OR last_block_revision=revision),
    CHECK ((source_kind='provider')=(source_id<>'')),
    CHECK ((source_kind='operator')=(request_id IS NOT NULL)),
    CHECK ((source_kind IN ('service_report','postfix','provider'))=(recipient_id IS NOT NULL AND event_ref<>'')),
    CHECK ((cause='legacy' AND source_kind='legacy') OR
           (cause IN ('manual_add','release') AND source_kind='operator') OR
           (cause IN ('hard_bounce','complaint') AND source_kind IN ('service_report','postfix','provider')))
);
CREATE TABLE IF NOT EXISTS internal_mail_suppression_events (
    namespace text NOT NULL,
    address text NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    blocked boolean NOT NULL,
    reason text NOT NULL CHECK (reason IN ('manual','bounced','complained')),
    cause text NOT NULL CHECK (cause IN ('legacy','manual_add','release','hard_bounce','complaint')),
    source_kind text NOT NULL CHECK (source_kind IN ('legacy','operator','service_report','postfix','provider')),
    source_id text NOT NULL DEFAULT '' CHECK (source_id='' OR source_id ~ '^[a-z][a-z0-9_-]{0,31}$'),
    recipient_id uuid,
    event_ref text NOT NULL DEFAULT '' CHECK (event_ref='' OR event_ref ~ '^[0-9a-f]{64}$'),
    request_id uuid,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY(namespace,address,revision),
    FOREIGN KEY(namespace,address) REFERENCES internal_mail_suppression_state(namespace,address),
    CHECK (reason<>'complained' OR blocked),
    CHECK ((cause='release')=NOT blocked),
    CHECK ((source_kind='provider')=(source_id<>'')),
    CHECK ((source_kind='operator')=(request_id IS NOT NULL)),
    CHECK ((source_kind IN ('service_report','postfix','provider'))=(recipient_id IS NOT NULL AND event_ref<>'')),
    CHECK ((cause='legacy' AND source_kind='legacy') OR
           (cause IN ('manual_add','release') AND source_kind='operator') OR
           (cause IN ('hard_bounce','complaint') AND source_kind IN ('service_report','postfix','provider')))
);
ALTER TABLE internal_mail_recipients ADD COLUMN IF NOT EXISTS suppression_revision bigint NOT NULL DEFAULT 0 CHECK (suppression_revision>=0);

-- Preserve known complaints even if an old writer later replaced their reason
-- with a bounce from another message. Never reinterpret managed/released state.
UPDATE internal_mail_suppressions s SET reason='complained'
WHERE s.reason<>'complained'
  AND NOT EXISTS (SELECT 1 FROM internal_mail_suppression_state x WHERE x.namespace=s.namespace AND x.address=s.address)
  AND EXISTS (SELECT 1 FROM internal_mail_recipients r WHERE r.namespace=s.namespace AND lower(r.address)=s.address
    AND (r.status='complained' OR EXISTS (SELECT 1 FROM internal_mail_events e WHERE e.namespace=r.namespace AND e.recipient_id=r.id AND e.kind='complained')));
INSERT INTO internal_mail_suppression_state(namespace,address,revision,last_block_revision,blocked,reason,cause,source_kind,created_at,updated_at)
SELECT namespace,address,1,1,true,reason,'legacy','legacy',created_at,created_at FROM internal_mail_suppressions ON CONFLICT DO NOTHING;
INSERT INTO internal_mail_suppression_events(namespace,address,revision,blocked,reason,cause,source_kind,created_at)
SELECT namespace,address,revision,blocked,reason,cause,source_kind,updated_at FROM internal_mail_suppression_state
WHERE cause='legacy' AND revision=1 ON CONFLICT DO NOTHING;

ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire','suppression_add','suppression_release'));

-- Restoring PostgreSQL cannot detect its own rewind. A fresh operator UUID,
-- supplied outside the restored snapshot before startup, installs this hold.
-- Generation mismatch quarantines old pending mail without rewriting custody.
CREATE TABLE IF NOT EXISTS internal_mail_recovery_runs (
    namespace text NOT NULL CHECK (namespace ~ '^[a-z][a-z0-9_-]{0,63}$'),
    recovery_id uuid NOT NULL CHECK (recovery_id<>'00000000-0000-0000-0000-000000000000'::uuid),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    PRIMARY KEY(namespace,recovery_id)
);
CREATE TABLE IF NOT EXISTS internal_mail_recovery_state (
    namespace text PRIMARY KEY,
    recovery_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision>0),
    held boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(updated_at)),
    FOREIGN KEY(namespace,recovery_id) REFERENCES internal_mail_recovery_runs(namespace,recovery_id)
);
ALTER TABLE internal_mail_recipients ADD COLUMN IF NOT EXISTS recovery_id uuid NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000'::uuid;
ALTER TABLE internal_mail_operations DROP CONSTRAINT IF EXISTS internal_mail_operations_action_check;
ALTER TABLE internal_mail_operations ADD CONSTRAINT internal_mail_operations_action_check
    CHECK (action IN ('reconcile','retention_apply','dkim_stage','dkim_verify','dkim_activate','dkim_retire','domain_reverify',
                      'delivery_pause','delivery_resume','delivery_drain','message_cancel','recipient_cancel','reputation_release','event_feed_replay',
                      'producer_rotate','producer_revoke','producer_suspend','producer_resume',
                      'encryption_activate','encryption_rewrap','encryption_retire','suppression_add','suppression_release','recovery_start','recovery_review','recovery_release'));
