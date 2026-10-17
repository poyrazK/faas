-- Application-owned PostgreSQL DDL. Install explicitly beside the consumer inbox.
-- Token and result are protected secrets; restrict table access to the worker role.
CREATE TABLE IF NOT EXISTS public.gregale_event_acknowledgment_intents (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 invocation_id uuid NOT NULL,
 replay_generation bigint NOT NULL CHECK(replay_generation>=0),
 attempt integer NOT NULL CHECK(attempt>0),
 identity_digest bytea NOT NULL CHECK(octet_length(identity_digest)=32),
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 capability_digest bytea NOT NULL CHECK(octet_length(capability_digest)=32),
 action_digest bytea NOT NULL CHECK(octet_length(action_digest)=32),
 app_slug text NOT NULL CHECK(octet_length(app_slug) BETWEEN 1 AND 256),
 lease_seconds integer NOT NULL CHECK(lease_seconds BETWEEN 1 AND 300),
 action text NOT NULL CHECK(action IN ('complete','retry','dead-letter')),
 reason text NOT NULL CHECK(reason='' OR (octet_length(reason)<=128 AND reason ~ '^[a-z0-9_.-]+$')),
 token text CHECK(token ~ '^[0-9a-f]{64}$'),
 result_body text CHECK(octet_length(result_body) BETWEEN 1 AND 1048576 AND result_body::jsonb IS NOT NULL),
 state text NOT NULL CHECK(state IN ('pending','unconfirmed','confirmed','superseded')),
 claim_token bytea CHECK(octet_length(claim_token)=32),
 claim_expires_at timestamptz,
 delivery_attempts bigint NOT NULL DEFAULT 0 CHECK(delivery_attempts BETWEEN 0 AND 2147483647),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_code text NOT NULL DEFAULT '' CHECK(last_code IN ('','unconfirmed','lease_lost','callback_confirmed','claim_replaced','receipt_committed')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(account_id,app_id,invocation_id,replay_generation,attempt),
 CHECK((claim_token IS NULL)=(claim_expires_at IS NULL)),
 CHECK((state IN ('pending','unconfirmed'))=(token IS NOT NULL)),
 CHECK((action='complete' AND reason='') OR (action<>'complete' AND reason<>'')),
 CHECK((state IN ('pending','unconfirmed') AND action='complete')=(result_body IS NOT NULL)),
 CHECK(state IN ('pending','unconfirmed') OR claim_token IS NULL)
);
CREATE INDEX IF NOT EXISTS gregale_event_ack_intents_due ON public.gregale_event_acknowledgment_intents(account_id,app_id,next_attempt_at,invocation_id) WHERE state IN ('pending','unconfirmed');
CREATE INDEX IF NOT EXISTS gregale_event_ack_intents_diagnostics ON public.gregale_event_acknowledgment_intents(account_id,app_id,updated_at DESC);
