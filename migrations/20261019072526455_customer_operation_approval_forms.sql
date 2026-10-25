-- migration-compat: expand
-- ADR-20261019001500: an approval form names the structured input an approver
-- submits with a decision, for one approval step of an Operation name in an
-- app environment.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS customer_operation_approval_forms (
    id           uuid        PRIMARY KEY,
    account_id   uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id       uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    scope        text        NOT NULL CHECK (length(scope) BETWEEN 1 AND 64),
    operation    text        NOT NULL CHECK (operation ~ '^[a-z][a-z0-9-]{0,63}$'),
    step         text        NOT NULL CHECK (step ~ '^[a-z][a-z0-9-]{0,63}$'),
    input_schema jsonb       NOT NULL CHECK (jsonb_typeof(input_schema) = 'object'),
    required     boolean     NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    UNIQUE (app_id, scope, operation, step)
);

-- +goose Down
DROP TABLE IF EXISTS customer_operation_approval_forms;
