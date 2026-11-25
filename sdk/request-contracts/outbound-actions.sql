-- Private application-owned action intents. Apply explicitly using a migration role.
CREATE TABLE IF NOT EXISTS public.gregale_outbound_actions (
 identity text PRIMARY KEY CHECK(identity ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK(jsonb_typeof(intent)='object'),
 version integer NOT NULL DEFAULT 0 CHECK(version >= 0),
 outcome jsonb NOT NULL CHECK(jsonb_typeof(outcome)='object'),
 state text NOT NULL CHECK(state IN ('prepared','unknown','pending','succeeded','failed','needs_review','cancelled')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(outcome->>'state'=state)
);
CREATE TABLE IF NOT EXISTS public.gregale_outbound_action_runs (
 action_id text NOT NULL REFERENCES public.gregale_outbound_actions(identity),
 run_id uuid NOT NULL,
 input_sha256 text NOT NULL CHECK(input_sha256 ~ '^[a-f0-9]{64}$'),
 PRIMARY KEY(action_id,run_id)
);
CREATE TABLE IF NOT EXISTS public.gregale_outbound_action_attempts (
 namespace text NOT NULL CHECK(namespace ~ '^[a-f0-9]{64}$'),
 request_id uuid NOT NULL,
 action_id text NOT NULL REFERENCES public.gregale_outbound_actions(identity),
 PRIMARY KEY(namespace,request_id)
);
