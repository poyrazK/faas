-- Customer-owned PostgreSQL schema, installed explicitly by the application owner.
-- The application inserts only; the managed relay updates delivery metadata.
CREATE TABLE IF NOT EXISTS public.gregale_outbox (
    event_id uuid PRIMARY KEY,
    event_type text NOT NULL CHECK (length(event_type) BETWEEN 1 AND 256),
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    accepted_at timestamptz,
    receipt_id uuid,
    invocation_id uuid,
    operation_id uuid,
    lease_token uuid,
    lease_until timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    blocked_code text,
    CONSTRAINT gregale_outbox_delivery_identity CHECK (
        (accepted_at IS NULL AND receipt_id IS NULL AND invocation_id IS NULL AND operation_id IS NULL)
        OR (accepted_at IS NOT NULL AND receipt_id IS NOT NULL
            AND (invocation_id IS NOT NULL)::integer + (operation_id IS NOT NULL)::integer = 1))
);
CREATE INDEX IF NOT EXISTS gregale_outbox_pending
    ON public.gregale_outbox(next_attempt_at, created_at, event_id)
    WHERE accepted_at IS NULL AND blocked_code IS NULL;

-- The database owner binds this outbox to exactly one Gregale source. Relay
-- credentials need SELECT here; they cannot choose or change the destination.
CREATE TABLE IF NOT EXISTS public.gregale_commit_binding (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    source_id uuid NOT NULL
);
