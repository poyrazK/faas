-- ADR-20261018210500: the account limits how many Operations of one name a
-- customer has in flight; admission queues the rest and apid releases them in
-- order as slots free.
--
-- Not CONCURRENTLY: pkg/db.MigrateUp runs every file in a transaction. The
-- Operations tables are small while Operations admission is in preview.
-- +goose Up
CREATE TABLE IF NOT EXISTS customer_operation_concurrency_policies (
    id                 uuid        PRIMARY KEY,
    account_id         uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id             uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope              text        NOT NULL CHECK (length(scope) BETWEEN 1 AND 64),
    operation          text        NOT NULL CHECK (operation ~ '^[a-z][a-z0-9-]{0,63}$'),
    platform_tenant_id uuid        REFERENCES platform_tenants(id) ON DELETE CASCADE,
    max_running        integer     NOT NULL CHECK (max_running BETWEEN 1 AND 1000),
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_concurrency_default_key
    ON customer_operation_concurrency_policies (app_id, scope, operation) WHERE platform_tenant_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS customer_operation_concurrency_customer_key
    ON customer_operation_concurrency_policies (app_id, scope, operation, platform_tenant_id) WHERE platform_tenant_id IS NOT NULL;

-- One row per queued Operation; the row goes when apid releases it.
CREATE TABLE IF NOT EXISTS customer_operation_queue (
    operation_id       uuid        PRIMARY KEY REFERENCES customer_operations(id) ON DELETE CASCADE,
    account_id         uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id             uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope              text        NOT NULL,
    operation          text        NOT NULL,
    platform_tenant_id uuid        NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    queued_at          timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS customer_operation_queue_group_idx
    ON customer_operation_queue (app_id, scope, operation, platform_tenant_id, queued_at, operation_id);
CREATE INDEX IF NOT EXISTS customer_operation_queue_tenant_idx
    ON customer_operation_queue (platform_tenant_id, queued_at);

-- Admission and release count a customer's in-flight Operations.
CREATE INDEX IF NOT EXISTS customer_operations_tenant_in_flight_idx
    ON customer_operations (platform_tenant_id, app_id)
    WHERE state IN ('accepted', 'running', 'requires_reconciliation');

-- +goose Down
DROP INDEX IF EXISTS customer_operations_tenant_in_flight_idx;
DROP TABLE IF EXISTS customer_operation_queue;
DROP TABLE IF EXISTS customer_operation_concurrency_policies;
