-- filename: 20261118172732100_product_analytics_events.sql
-- ADR 20261117084505: product analytics. analytics_events keeps one row per
-- accepted track() event for the plan's analytics retention
-- (AnalyticsRetentionDays), with the customer, an optional end-user subject
-- hash (sha256 of environment and subject; the subject is never stored),
-- bounded string properties and the flag decisions of the event's request.
-- analytics_property_keys bounds distinct property keys per environment.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS analytics_events (
    id bigserial PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    customer_id uuid NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    subject_hash bytea,
    name text NOT NULL,
    at timestamptz NOT NULL,
    value bigint NOT NULL DEFAULT 0,
    properties jsonb NOT NULL DEFAULT '{}'::jsonb,
    flags jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT analytics_events_chk CHECK (
        name ~ '^[a-z][a-z0-9_.-]{0,63}$'
        AND (subject_hash IS NULL OR length(subject_hash) = 32)
        AND jsonb_typeof(properties) = 'object' AND jsonb_typeof(flags) = 'object')
);
CREATE INDEX IF NOT EXISTS analytics_events_env_name_at_idx ON analytics_events (environment_id, name, at);
CREATE INDEX IF NOT EXISTS analytics_events_customer_at_idx ON analytics_events (customer_id, at);
CREATE INDEX IF NOT EXISTS analytics_events_subject_at_idx ON analytics_events (environment_id, subject_hash, at) WHERE subject_hash IS NOT NULL;
CREATE INDEX IF NOT EXISTS analytics_events_at_idx ON analytics_events (at);

CREATE TABLE IF NOT EXISTS analytics_property_keys (
    environment_id uuid NOT NULL REFERENCES project_environments(id) ON DELETE CASCADE,
    key text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (environment_id, key),
    CONSTRAINT analytics_property_keys_chk CHECK (key ~ '^[a-z][a-z0-9_]{0,63}$')
);

-- +goose Down
DROP TABLE IF EXISTS analytics_property_keys;
DROP TABLE IF EXISTS analytics_events;
