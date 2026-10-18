-- ADR-586. Customer-owned schema; install explicitly as the database owner.
-- Receipts and business writes share one transaction. No processing lease or
-- incomplete receipt is committed. Retain receipts while operations can replay.
CREATE TABLE IF NOT EXISTS public.gregale_operation_inbox (
    operation_id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    platform_tenant_id uuid,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    response_body text NOT NULL CHECK (
        octet_length(response_body) BETWEEN 1 AND 1048576
        AND COALESCE(jsonb_typeof(response_body::jsonb) = 'object'
        AND response_body::jsonb->'gregale_operation_result' = '1'::jsonb
        AND response_body::jsonb ? 'result'
        AND jsonb_typeof(response_body::jsonb->'effects') = 'array', false)
    ),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
