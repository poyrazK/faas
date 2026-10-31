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
 state text NOT NULL CHECK(state IN ('pending','unconfirmed','parked','confirmed','superseded')),
 claim_token bytea CHECK(octet_length(claim_token)=32),
 claim_expires_at timestamptz,
 delivery_attempts bigint NOT NULL DEFAULT 0 CHECK(delivery_attempts BETWEEN 0 AND 2147483647),
 retry_policy_version integer,
 retry_initial_backoff_ms bigint,
 retry_max_backoff_ms bigint,
 retry_max_delivery_attempts bigint,
 delivery_attempt_limit bigint,
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_code text NOT NULL DEFAULT '' CHECK(last_code IN ('','unconfirmed','lease_lost','callback_confirmed','claim_replaced','receipt_committed','attempts_exhausted')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(account_id,app_id,invocation_id,replay_generation,attempt),
 CONSTRAINT gregale_event_acknowledgment_intents_check CHECK((claim_token IS NULL)=(claim_expires_at IS NULL)),
 CONSTRAINT gregale_event_ack_intents_payload_check CHECK((state IN ('pending','unconfirmed','parked'))=(token IS NOT NULL)),
 CONSTRAINT gregale_event_acknowledgment_intents_check2 CHECK((action='complete' AND reason='') OR (action<>'complete' AND reason<>'')),
 CONSTRAINT gregale_event_ack_intents_result_check CHECK((state IN ('pending','unconfirmed','parked') AND action='complete')=(result_body IS NOT NULL)),
 CONSTRAINT gregale_event_ack_intents_claim_state_check CHECK(state IN ('pending','unconfirmed') OR claim_token IS NULL),
 CONSTRAINT gregale_event_ack_intents_retry_policy_check CHECK(
  (retry_policy_version IS NULL AND retry_initial_backoff_ms IS NULL AND retry_max_backoff_ms IS NULL AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL)
  OR (retry_policy_version IS NOT NULL AND retry_initial_backoff_ms IS NOT NULL AND retry_max_backoff_ms IS NOT NULL
   AND retry_initial_backoff_ms BETWEEN 1 AND 3600000 AND retry_max_backoff_ms BETWEEN retry_initial_backoff_ms AND 3600000
   AND ((retry_policy_version=1 AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL)
    OR (retry_policy_version=2 AND retry_max_delivery_attempts IS NOT NULL AND delivery_attempt_limit IS NOT NULL
     AND retry_max_delivery_attempts BETWEEN 1 AND 2147483647 AND delivery_attempt_limit BETWEEN retry_max_delivery_attempts AND 2147483647)))),
 CONSTRAINT gregale_event_ack_intents_parked_check CHECK(
  (state='parked')=(last_code='attempts_exhausted')
  AND (state<>'parked' OR (retry_policy_version IS NOT NULL AND retry_policy_version=2
   AND delivery_attempt_limit IS NOT NULL AND delivery_attempts>=delivery_attempt_limit)))
);
CREATE INDEX IF NOT EXISTS gregale_event_ack_intents_due ON public.gregale_event_acknowledgment_intents(account_id,app_id,next_attempt_at,invocation_id) WHERE state IN ('pending','unconfirmed');
CREATE INDEX IF NOT EXISTS gregale_event_ack_intents_diagnostics ON public.gregale_event_acknowledgment_intents(account_id,app_id,updated_at DESC);
