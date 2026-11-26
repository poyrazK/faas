-- Application-owned clarification grants. Install alongside the pipeline gate store.
CREATE TABLE IF NOT EXISTS public.gregale_pipeline_task_handoffs (
 namespace text NOT NULL,
 id uuid NOT NULL,
 issuer_hash text NOT NULL CHECK (issuer_hash ~ '^[0-9a-f]{64}$'),
 reviewer_hash text NOT NULL CHECK (reviewer_hash ~ '^[0-9a-f]{64}$'),
 task_id uuid NOT NULL,
 tool text NOT NULL,
 run_id uuid NOT NULL,
 gate_id text NOT NULL CHECK (gate_id ~ '^[0-9a-f]{64}$'),
 binding_sha256 text NOT NULL CHECK (binding_sha256 ~ '^[0-9a-f]{64}$'),
 expires_at timestamptz NOT NULL,
 revoked_at timestamptz,
 PRIMARY KEY (namespace, id)
);
CREATE INDEX IF NOT EXISTS gregale_pipeline_task_handoffs_expiry
 ON public.gregale_pipeline_task_handoffs (expires_at);
CREATE INDEX IF NOT EXISTS gregale_pipeline_task_handoffs_reviewer
 ON public.gregale_pipeline_task_handoffs (namespace, reviewer_hash, (md5(id::text)) COLLATE "C")
 WHERE revoked_at IS NULL;
