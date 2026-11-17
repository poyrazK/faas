-- Application-owned recovery plans, attempt bindings and atomic SQL effect receipts.
CREATE TABLE IF NOT EXISTS public.gregale_compensation_plans (
 identity text PRIMARY KEY CHECK(identity ~ '^[a-f0-9]{64}$'),
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS public.gregale_compensation_attempts (
 namespace text NOT NULL CHECK(namespace ~ '^[a-f0-9]{64}$'),
 request_id uuid NOT NULL,
 plan_id text NOT NULL REFERENCES public.gregale_compensation_plans(identity),
 PRIMARY KEY(namespace,request_id)
);
CREATE TABLE IF NOT EXISTS public.gregale_compensation_effects (
 identity text PRIMARY KEY CHECK(identity ~ '^[a-f0-9]{64}$'),
 command_sha256 text NOT NULL CHECK(command_sha256 ~ '^[a-f0-9]{64}$'),
 outcome jsonb NOT NULL CHECK(jsonb_typeof(outcome)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE IF NOT EXISTS public.gregale_compensation_runs (
 namespace text NOT NULL CHECK(namespace ~ '^[a-f0-9]{64}$'),
 run_id uuid NOT NULL,
 request_id uuid NOT NULL,
 plan_id text NOT NULL REFERENCES public.gregale_compensation_plans(identity),
 PRIMARY KEY(namespace,run_id),
 UNIQUE(namespace,request_id),
 FOREIGN KEY(namespace,request_id) REFERENCES public.gregale_compensation_attempts(namespace,request_id)
);
