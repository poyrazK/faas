-- filename: 20261021164412336_product_control_plan_history.sql

-- ADR-20261021154511: every Product Control assignment change is appended,
-- so a month's statement charges the highest monthly fee among the plans the
-- customer held in it, prorated from their first assignment. app_id has no
-- foreign key, so purging an app keeps the history with the billing records
-- it priced (ADR-20261020090607); account and tenant deletion erase it.
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS product_control_plan_history (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id           uuid NOT NULL,
    tenant_id        uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    revision         text NOT NULL,
    plan             text NOT NULL,
    first_assignment boolean NOT NULL,
    effective_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT product_control_plan_history_revision_chk CHECK (revision ~ '^[0-9a-f]{64}$'),
    CONSTRAINT product_control_plan_history_plan_chk CHECK (plan ~ '^[a-z][a-z0-9-]{0,62}$')
);
CREATE INDEX IF NOT EXISTS product_control_plan_history_customer_idx
  ON product_control_plan_history (app_id, tenant_id, effective_at);
COMMENT ON TABLE product_control_plan_history IS
  'Each Product Control customer assignment to a catalog revision and plan, from effective_at; prices monthly plan fees (ADR-20261021154511).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS product_control_plan_history;
-- +goose StatementEnd
