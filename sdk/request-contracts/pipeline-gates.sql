CREATE TABLE IF NOT EXISTS public.gregale_pipeline_gates (
 id text PRIMARY KEY CHECK (id ~ '^[a-f0-9]{64}$'),
 intent jsonb NOT NULL CHECK (jsonb_typeof(intent)='object'),
 expires_at timestamptz NOT NULL,
 checked_at timestamptz NOT NULL DEFAULT '-infinity',
 decision jsonb,
 delivery text NOT NULL DEFAULT 'pending' CHECK (delivery IN ('pending','delivered','closed'))
);
CREATE INDEX IF NOT EXISTS gregale_pipeline_gates_pending ON public.gregale_pipeline_gates(delivery,expires_at);
