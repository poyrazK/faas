-- migration-compat: expand
-- ADR-20261018161833: a schedule admits ordinary Operations for one customer,
-- once at a fixed time or on a cron timetable. apid fires due rows; the lease
-- lets one replica fire a row while the others skip it.
-- +goose Up
CREATE TABLE IF NOT EXISTS customer_operation_schedules (
    id                 uuid        PRIMARY KEY,
    account_id         uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id             uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    platform_tenant_id uuid        NOT NULL REFERENCES platform_tenants(id) ON DELETE CASCADE,
    definition_id      uuid        NOT NULL REFERENCES customer_operation_definitions(id) ON DELETE CASCADE,
    input              jsonb       NOT NULL,
    kind               text        NOT NULL CHECK (kind IN ('once','cron')),
    at                 timestamptz,
    cron               text        NOT NULL DEFAULT '',
    timezone           text        NOT NULL DEFAULT '',
    state              text        NOT NULL CHECK (state IN ('active','paused','completed','failed','cancelled')),
    next_fire_at       timestamptz,
    fires              bigint      NOT NULL DEFAULT 0 CHECK (fires >= 0),
    last_fired_at      timestamptz,
    last_operation_id  uuid,
    last_error         text        NOT NULL DEFAULT '' CHECK (octet_length(last_error) <= 512),
    lease_token        uuid,
    lease_until        timestamptz,
    created_at         timestamptz NOT NULL,
    updated_at         timestamptz NOT NULL,
    CHECK ((kind = 'once' AND at IS NOT NULL AND cron = '') OR (kind = 'cron' AND at IS NULL AND cron <> '' AND timezone <> '')),
    CHECK ((state = 'active') = (next_fire_at IS NOT NULL) OR state = 'paused')
);

CREATE INDEX IF NOT EXISTS customer_operation_schedules_due_idx
    ON customer_operation_schedules (next_fire_at) WHERE state = 'active';
CREATE INDEX IF NOT EXISTS customer_operation_schedules_owner_idx
    ON customer_operation_schedules (account_id, platform_tenant_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS customer_operation_schedules;
