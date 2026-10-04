-- +goose Up
-- +goose StatementBegin
-- ADR-521. Execution ledgers stay authoritative for execution, while a durable
-- customer operation connects identity, progress, business outcome and delivery.
CREATE TABLE IF NOT EXISTS customer_operation_definitions (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 64),
    name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{0,63}$'),
    revision text NOT NULL CHECK (revision ~ '^[0-9a-f]{64}$'),
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE RESTRICT,
    release_id text NOT NULL DEFAULT '',
    spec jsonb NOT NULL CHECK (jsonb_typeof(spec) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (app_id, scope, name, deployment_id),
    CHECK (spec->>'name' = name)
);

CREATE TABLE IF NOT EXISTS customer_operations (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    platform_tenant_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    definition_id uuid NOT NULL REFERENCES customer_operation_definitions(id) ON DELETE RESTRICT,
    current_invocation_id uuid NOT NULL REFERENCES invocations(id) ON DELETE RESTRICT,
    state text NOT NULL CHECK (state IN ('accepted','running','succeeded','failed','cancelled','requires_reconciliation')),
    record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (record->>'id' = id::text),
    CHECK (record->>'state' = state),
    CHECK (record->>'account_id' = account_id::text),
    CHECK (record->>'app_id' = app_id::text),
    CHECK (record->>'platform_tenant_id' = platform_tenant_id::text),
    CHECK (record->>'definition_id' = definition_id::text),
    CHECK (record->>'current_invocation_id' = current_invocation_id::text)
);
CREATE INDEX IF NOT EXISTS customer_operations_pending_account_idx ON customer_operations(account_id)
    WHERE state IN ('accepted','running','requires_reconciliation');
CREATE INDEX IF NOT EXISTS customer_operations_tenant_idx ON customer_operations(platform_tenant_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS customer_operations_retention_idx ON customer_operations(expires_at);

CREATE TABLE IF NOT EXISTS customer_operation_executions (
    operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
    generation integer NOT NULL CHECK (generation > 0),
    invocation_id uuid NOT NULL UNIQUE REFERENCES invocations(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(operation_id,generation)
);

-- Deliberately no FK to customer_operations: this receipt is a tombstone after
-- result expiry, so deleting bulky result state cannot admit duplicate work.
CREATE TABLE IF NOT EXISTS customer_operation_idempotency (
    scope_digest text PRIMARY KEY CHECK (scope_digest ~ '^[0-9a-f]{64}$'),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    operation_id uuid NOT NULL,
    fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
    expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS customer_operation_idempotency_retention_idx ON customer_operation_idempotency(expires_at);

CREATE TABLE IF NOT EXISTS customer_operation_events (
    operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    event_type text NOT NULL CHECK (event_type IN ('accepted','running','progress','succeeded','failed','cancellation_requested','cancelled','reconciliation_required','recovery_requested','delivery_changed','result_expired')),
    execution_id uuid REFERENCES invocations(id) ON DELETE RESTRICT,
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    created_at timestamptz NOT NULL,
    PRIMARY KEY(operation_id,sequence)
);
CREATE TABLE IF NOT EXISTS customer_operation_reports (
 operation_id uuid NOT NULL REFERENCES customer_operations(id) ON DELETE CASCADE,
 execution_id uuid NOT NULL REFERENCES invocations(id) ON DELETE RESTRICT,
 attempt integer NOT NULL CHECK (attempt > 0),
 report_id text NOT NULL CHECK (octet_length(report_id) BETWEEN 1 AND 128),
 fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
 PRIMARY KEY(operation_id, execution_id, attempt, report_id)
);
-- +goose StatementEnd

-- +goose Down
-- Forward-only. Rolling back the application must preserve admitted work,
-- identity tombstones, business outcomes and durable completion history.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
