-- filename: 20261011230231383_workflow_readiness_subscriptions.sql

-- +goose Up
CREATE TABLE IF NOT EXISTS customer_workflow_readiness_subscriptions (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 tenant_id uuid REFERENCES platform_tenants(id) ON DELETE CASCADE,
 operator boolean NOT NULL,
 scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 40 AND scope ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
 created_at timestamptz NOT NULL DEFAULT now(),
 next_evaluation_at timestamptz NOT NULL,
 lease_token uuid,
 lease_until timestamptz,
 CHECK (operator OR tenant_id IS NOT NULL),
 CHECK ((lease_token IS NULL) = (lease_until IS NULL))
);
CREATE INDEX IF NOT EXISTS customer_workflow_readiness_subscription_due ON customer_workflow_readiness_subscriptions (next_evaluation_at, id);
CREATE INDEX IF NOT EXISTS customer_workflow_readiness_subscription_owner ON customer_workflow_readiness_subscriptions (account_id, app_id, operator, scope);
CREATE TABLE IF NOT EXISTS customer_workflow_readiness_notifications (
 subscription_id uuid NOT NULL REFERENCES customer_workflow_readiness_subscriptions(id) ON DELETE CASCADE,
 sequence bigint NOT NULL CHECK (sequence > 0),
 payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
 PRIMARY KEY (subscription_id, sequence)
);

-- +goose Down
DROP TABLE IF EXISTS customer_workflow_readiness_notifications;
DROP TABLE IF EXISTS customer_workflow_readiness_subscriptions;
