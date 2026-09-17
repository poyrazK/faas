-- +goose Up
-- +goose StatementBegin

-- First-class queue bindings (queue consumers/autoscaling foundation). A
-- binding is app-scoped configuration; invocation rows remain the durable
-- message ledger. Keeping the account id denormalized makes tenant-scoped
-- reads and future consumer claims index-only.
CREATE TABLE IF NOT EXISTS queue_bindings (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id           uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name             text NOT NULL,
    queue_name       text NOT NULL,
    mode             text NOT NULL DEFAULT 'pull',
    workload_class   text NOT NULL DEFAULT 'worker',
    enabled          boolean NOT NULL DEFAULT true,
    max_concurrency  integer NOT NULL DEFAULT 1,
    retry_policy     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT queue_bindings_name_shape CHECK (name ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT queue_bindings_queue_name_shape CHECK (queue_name ~ '^[a-z][a-z0-9-]{0,62}$'),
    CONSTRAINT queue_bindings_mode_chk CHECK (mode IN ('pull', 'push')),
    CONSTRAINT queue_bindings_workload_class_chk CHECK (workload_class IN ('worker', 'job')),
    CONSTRAINT queue_bindings_max_concurrency_chk CHECK (max_concurrency BETWEEN 1 AND 10000),
    CONSTRAINT queue_bindings_retry_policy_object_chk CHECK (jsonb_typeof(retry_policy) = 'object')
);

CREATE UNIQUE INDEX IF NOT EXISTS queue_bindings_app_name_uniq
    ON queue_bindings (app_id, name);
CREATE UNIQUE INDEX IF NOT EXISTS queue_bindings_app_queue_name_uniq
    ON queue_bindings (app_id, queue_name);
CREATE INDEX IF NOT EXISTS queue_bindings_account_app_idx
    ON queue_bindings (account_id, app_id, created_at, id);
CREATE INDEX IF NOT EXISTS queue_bindings_enabled_idx
    ON queue_bindings (app_id, enabled) WHERE enabled;

-- +goose StatementEnd

-- +goose Down
-- Queue bindings are configuration, but dropping them would make a rollback
-- silently disable consumers. Keep this migration forward-only like the other
-- scheduler-owned subscriber tables.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd

