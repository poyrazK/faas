-- Optional application-owned command receipts. Install explicitly after the intent schema.
-- Independent of intent retention: pruning an intent must not erase command deduplication.
CREATE TABLE IF NOT EXISTS public.gregale_event_ack_intent_policy_adoptions (
 account_id uuid NOT NULL,
 app_id uuid NOT NULL,
 command_id uuid NOT NULL,
 invocation_id uuid NOT NULL,
 replay_generation bigint NOT NULL CHECK(replay_generation>=0),
 attempt integer NOT NULL CHECK(attempt>0),
 request_digest bytea NOT NULL CHECK(octet_length(request_digest)=32),
 outcome text NOT NULL CHECK(outcome IN ('adopted','conflict','not_found')),
 intent_status text CHECK(octet_length(intent_status) BETWEEN 1 AND 8192 AND jsonb_typeof(intent_status::jsonb)='object'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK(isfinite(recorded_at)),
 PRIMARY KEY(account_id,app_id,command_id),
 CHECK((outcome='adopted')=(intent_status IS NOT NULL))
);
