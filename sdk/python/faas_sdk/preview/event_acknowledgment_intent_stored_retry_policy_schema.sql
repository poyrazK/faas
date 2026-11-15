-- Apply once, atomically in an application migration transaction, before upgrading intent workers.
-- Existing intents remain unbound. This does not reset attempts, claims, due times or saved callbacks.
ALTER TABLE public.gregale_event_acknowledgment_intents
 ADD COLUMN retry_policy_version integer,
 ADD COLUMN retry_initial_backoff_ms bigint,
 ADD COLUMN retry_max_backoff_ms bigint,
 ADD CONSTRAINT gregale_event_ack_intents_retry_policy_check CHECK(
  (retry_policy_version IS NULL AND retry_initial_backoff_ms IS NULL AND retry_max_backoff_ms IS NULL)
  OR (retry_policy_version IS NOT NULL AND retry_initial_backoff_ms IS NOT NULL AND retry_max_backoff_ms IS NOT NULL
   AND retry_policy_version=1 AND retry_initial_backoff_ms BETWEEN 1 AND 3600000
   AND retry_max_backoff_ms BETWEEN retry_initial_backoff_ms AND 3600000));
