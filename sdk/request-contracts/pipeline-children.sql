CREATE TABLE IF NOT EXISTS public.gregale_pipeline_children (
 identity text PRIMARY KEY CHECK (identity ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 state jsonb NOT NULL CHECK (jsonb_typeof(state)='object'),
 revision bigint NOT NULL DEFAULT 0 CHECK (revision>=0),
 expires_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT '-infinity'
);
