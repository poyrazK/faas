-- Application-owned start receipts. Apply explicitly with the migration role.
CREATE TABLE IF NOT EXISTS public.gregale_pipeline_tool_starts (
 identity text PRIMARY KEY CHECK(identity ~ '^[a-f0-9]{64}$'),
 receipt jsonb NOT NULL CHECK(jsonb_typeof(receipt)='object'),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
