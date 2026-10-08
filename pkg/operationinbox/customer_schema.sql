-- ADR-713. Application-owned schema; install explicitly as the database owner.
-- Retain receipts while the original customer Operation can replay.
CREATE TABLE IF NOT EXISTS public.gregale_customer_operation_inbox (
    operation_id uuid PRIMARY KEY,
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    platform_tenant_id uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    response_body text NOT NULL CHECK (
        octet_length(response_body) BETWEEN 1 AND 1048576
        AND response_body::jsonb IS NOT NULL
    ),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- ADR-715. Pending public milestones share the business/receipt transaction.
-- Install this additive table before opting a definition into milestones.
CREATE TABLE IF NOT EXISTS public.gregale_customer_operation_milestones (
    operation_id uuid NOT NULL REFERENCES public.gregale_customer_operation_inbox(operation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    id uuid NOT NULL,
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,63}$'),
    payload text NOT NULL CHECK (octet_length(payload) BETWEEN 1 AND 8192 AND payload::json IS NOT NULL),
    occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at)),
    acknowledged_at timestamptz CHECK (acknowledged_at IS NULL OR isfinite(acknowledged_at)),
    PRIMARY KEY (operation_id, id)
);

-- ADR-719. State revisions and their publication outbox share the business
-- transaction, so recovery can replay a committed update without rerunning it.
CREATE TABLE IF NOT EXISTS public.gregale_customer_operation_workflow_state_counters (
    platform_tenant_id uuid NOT NULL,
    workflow text NOT NULL CHECK (workflow ~ '^[a-z][a-z0-9-]{0,62}$'),
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 256 AND instance_id !~ '[\x00-\x1f\x7f]'),
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    last_state text CHECK (last_state IS NULL OR last_state ~ '^[a-z][a-z0-9-]{0,63}$'),
    PRIMARY KEY (platform_tenant_id, workflow, instance_id)
);

-- Upgrade installations that already use the revision counter.
ALTER TABLE public.gregale_customer_operation_workflow_state_counters
    ADD COLUMN IF NOT EXISTS last_state text;

CREATE TABLE IF NOT EXISTS public.gregale_customer_operation_workflow_states (
    operation_id uuid NOT NULL REFERENCES public.gregale_customer_operation_inbox(operation_id)
        ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    id uuid NOT NULL,
    platform_tenant_id uuid NOT NULL,
    workflow text NOT NULL CHECK (workflow ~ '^[a-z][a-z0-9-]{0,62}$'),
    instance_id text NOT NULL CHECK (octet_length(instance_id) BETWEEN 1 AND 256 AND instance_id !~ '[\x00-\x1f\x7f]'),
    from_state text NOT NULL DEFAULT '' CHECK (from_state = '' OR from_state ~ '^[a-z][a-z0-9-]{0,63}$'),
    state text NOT NULL CHECK (state ~ '^[a-z][a-z0-9-]{0,63}$'),
    revision bigint NOT NULL CHECK (revision BETWEEN 1 AND 9007199254740991),
    occurred_at timestamptz NOT NULL CHECK (isfinite(occurred_at)),
    acknowledged_at timestamptz CHECK (acknowledged_at IS NULL OR isfinite(acknowledged_at)),
    PRIMARY KEY (operation_id, id),
    UNIQUE (platform_tenant_id, workflow, instance_id, revision)
);

-- Upgrade installations created by the earlier workflow state snapshot schema.
ALTER TABLE public.gregale_customer_operation_workflow_states
    ADD COLUMN IF NOT EXISTS from_state text NOT NULL DEFAULT '';

-- Preserve the latest known app state on the counter, independently of
-- per-Operation outbox rows that may be removed after their receipt expires.
UPDATE public.gregale_customer_operation_workflow_state_counters c
SET last_state = (
    SELECT s.state
    FROM public.gregale_customer_operation_workflow_states s
    WHERE s.platform_tenant_id = c.platform_tenant_id
      AND s.workflow = c.workflow
      AND s.instance_id = c.instance_id
    ORDER BY s.revision DESC
    LIMIT 1
)
WHERE c.last_state IS NULL
  AND EXISTS (
    SELECT 1
    FROM public.gregale_customer_operation_workflow_states s
    WHERE s.platform_tenant_id = c.platform_tenant_id
      AND s.workflow = c.workflow
      AND s.instance_id = c.instance_id
  );
