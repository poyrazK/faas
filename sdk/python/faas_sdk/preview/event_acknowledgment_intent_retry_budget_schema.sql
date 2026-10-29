-- Apply once in an application migration transaction after the v1 policy-column upgrade.
-- No backfill enables budgets; unbound/v1 rows retain unlimited recovery.
SET LOCAL lock_timeout = '5s';
ALTER TABLE public.gregale_event_acknowledgment_intents
 ADD COLUMN retry_max_delivery_attempts bigint,
 ADD COLUMN delivery_attempt_limit bigint,
 DROP CONSTRAINT gregale_event_acknowledgment_intents_state_check,
 DROP CONSTRAINT gregale_event_acknowledgment_intents_last_code_check,
 DROP CONSTRAINT gregale_event_acknowledgment_intents_check1,
 DROP CONSTRAINT gregale_event_acknowledgment_intents_check3,
 DROP CONSTRAINT gregale_event_acknowledgment_intents_check4,
 DROP CONSTRAINT gregale_event_ack_intents_retry_policy_check,
 ADD CONSTRAINT gregale_event_acknowledgment_intents_state_check CHECK(state IN ('pending','unconfirmed','parked','confirmed','superseded')),
 ADD CONSTRAINT gregale_event_acknowledgment_intents_last_code_check CHECK(last_code IN ('','unconfirmed','lease_lost','callback_confirmed','claim_replaced','receipt_committed','attempts_exhausted')),
 ADD CONSTRAINT gregale_event_ack_intents_payload_check CHECK((state IN ('pending','unconfirmed','parked'))=(token IS NOT NULL)),
 ADD CONSTRAINT gregale_event_ack_intents_result_check CHECK((state IN ('pending','unconfirmed','parked') AND action='complete')=(result_body IS NOT NULL)),
 ADD CONSTRAINT gregale_event_ack_intents_claim_state_check CHECK(state IN ('pending','unconfirmed') OR claim_token IS NULL),
 ADD CONSTRAINT gregale_event_ack_intents_retry_policy_check CHECK(
  (retry_policy_version IS NULL AND retry_initial_backoff_ms IS NULL AND retry_max_backoff_ms IS NULL AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL)
  OR (retry_policy_version IS NOT NULL AND retry_initial_backoff_ms IS NOT NULL AND retry_max_backoff_ms IS NOT NULL
   AND retry_initial_backoff_ms BETWEEN 1 AND 3600000 AND retry_max_backoff_ms BETWEEN retry_initial_backoff_ms AND 3600000
   AND ((retry_policy_version=1 AND retry_max_delivery_attempts IS NULL AND delivery_attempt_limit IS NULL)
    OR (retry_policy_version=2 AND retry_max_delivery_attempts IS NOT NULL AND delivery_attempt_limit IS NOT NULL
     AND retry_max_delivery_attempts BETWEEN 1 AND 2147483647 AND delivery_attempt_limit BETWEEN retry_max_delivery_attempts AND 2147483647)))),
 ADD CONSTRAINT gregale_event_ack_intents_parked_check CHECK(
  (state='parked')=(last_code='attempts_exhausted')
  AND (state<>'parked' OR (retry_policy_version IS NOT NULL AND retry_policy_version=2
   AND delivery_attempt_limit IS NOT NULL AND delivery_attempts>=delivery_attempt_limit)));
