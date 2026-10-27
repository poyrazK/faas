-- Apply atomically in an application migration transaction before upgrading workers.
ALTER TABLE public.gregale_event_outbox
 DROP CONSTRAINT gregale_event_outbox_last_code_check,
 ADD CONSTRAINT gregale_event_outbox_last_code_check CHECK(last_code IN ('','unconfirmed','publication_confirmed','publication_rejected','retry_window_expired','attempts_exhausted'));
ALTER TABLE public.gregale_event_outbox_controls
 DROP CONSTRAINT gregale_event_outbox_controls_action_check,
 DROP CONSTRAINT gregale_event_outbox_controls_outcome_check,
 DROP CONSTRAINT gregale_event_outbox_controls_check,
 ADD CONSTRAINT gregale_event_outbox_controls_action_check CHECK(action IN ('reconcile','resume','set_retry_policy')),
 ADD CONSTRAINT gregale_event_outbox_controls_outcome_check CHECK(outcome IN ('published','already_published','unavailable','conflict','unverified','resumed','rejected','policy_updated')),
 ADD CONSTRAINT gregale_event_outbox_controls_check CHECK((action='resume' AND outcome IN ('resumed','rejected')) OR (action='reconcile' AND outcome IN ('published','already_published','unavailable','conflict','unverified')) OR (action='set_retry_policy' AND outcome IN ('policy_updated','rejected')));

-- A binding belongs to the retained publication; audit receipts remain independent.
CREATE TABLE IF NOT EXISTS public.gregale_event_outbox_retry_policies (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 producer_key text NOT NULL,
 version integer NOT NULL CHECK(version=1),
 revision bigint NOT NULL CHECK(revision>0),
 initial_backoff_ms bigint NOT NULL CHECK(initial_backoff_ms BETWEEN 1 AND 3600000),
 max_backoff_ms bigint NOT NULL CHECK(max_backoff_ms BETWEEN initial_backoff_ms AND 3600000),
 max_attempts bigint NOT NULL CHECK(max_attempts BETWEEN 0 AND 2147483647),
 initial_digest bytea NOT NULL CHECK(octet_length(initial_digest)=32),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(updated_at)),
 PRIMARY KEY(account_id,app_id,producer_key),
 FOREIGN KEY(account_id,app_id,producer_key) REFERENCES public.gregale_event_outbox(account_id,app_id,producer_key) ON DELETE CASCADE
);
