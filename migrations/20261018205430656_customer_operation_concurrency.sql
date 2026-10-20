-- migration-compat: expand
-- ADR-20261018210500: the account limits how many Operations of one name a
-- customer has in flight; admission queues the rest and apid releases them in
-- order as slots free. The previous release never reads these tables.
-- Each statement is its own short transaction (NO TRANSACTION): no step
-- locks more than three existing tables or scans one while blocking writes,
-- constraints are added NOT VALID and validated separately, and every step
-- is idempotent, so an interrupted run resumes.
-- +goose NO TRANSACTION
-- +goose Up
SET lock_timeout = '5s';
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
    platform_tenant_id uuid        NOT NULL,
    queued_at          timestamptz NOT NULL
);
-- +goose StatementBegin
DO $guard$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'customer_operation_queue_platform_tenant_id_fkey'
                   AND conrelid = 'customer_operation_queue'::regclass) THEN
        ALTER TABLE customer_operation_queue ADD CONSTRAINT customer_operation_queue_platform_tenant_id_fkey FOREIGN KEY (platform_tenant_id) REFERENCES platform_tenants(id) ON DELETE CASCADE NOT VALID;
    END IF;
END
$guard$;
-- +goose StatementEnd
ALTER TABLE customer_operation_queue VALIDATE CONSTRAINT customer_operation_queue_platform_tenant_id_fkey;
CREATE INDEX IF NOT EXISTS customer_operation_queue_group_idx
    ON customer_operation_queue (app_id, scope, operation, platform_tenant_id, queued_at, operation_id);
CREATE INDEX IF NOT EXISTS customer_operation_queue_tenant_idx
    ON customer_operation_queue (platform_tenant_id, queued_at);
RESET lock_timeout;

-- +goose Down
DROP TABLE IF EXISTS customer_operation_queue;
DROP TABLE IF EXISTS customer_operation_concurrency_policies;
