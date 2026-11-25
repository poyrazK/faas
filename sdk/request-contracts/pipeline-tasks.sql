-- Store only the owned Task-to-pipeline binding. Apply with the migration role.
CREATE TABLE IF NOT EXISTS public.gregale_pipeline_tasks (
 namespace text NOT NULL,
 owner_hash text NOT NULL CHECK (owner_hash ~ '^[0-9a-f]{64}$'),
 tool_name text NOT NULL,
 request_id uuid NOT NULL,
 receipt_identity text NOT NULL CHECK (receipt_identity ~ '^[0-9a-f]{64}$'),
 task_id uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
 expires_at timestamptz NOT NULL,
 PRIMARY KEY (namespace, owner_hash, tool_name, request_id),
 UNIQUE (namespace, task_id)
);
CREATE INDEX IF NOT EXISTS gregale_pipeline_tasks_expiry ON public.gregale_pipeline_tasks (expires_at);
