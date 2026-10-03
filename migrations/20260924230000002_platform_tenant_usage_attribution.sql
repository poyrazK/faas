-- +goose Up
-- +goose StatementBegin

-- Record the verified customer at request time. Existing event rows stay NULL:
-- inferring a historical tenant from today's consumer link would rewrite usage.
ALTER TABLE api_consumer_usage_events ADD COLUMN IF NOT EXISTS platform_tenant_id uuid;
-- The aggregate's composite FK below validates every new attributed event
-- in the same transaction. Avoid validating a redundant FK across the
-- existing, potentially large append-only event table during rollout.

-- Keep the existing app-consumer minute ledger and its primary key unchanged.
-- A consumer linked partway through a minute can contribute to both the
-- unassigned app ledger and this tenant-specific aggregate without conflation.
CREATE TABLE IF NOT EXISTS platform_tenant_usage_minutes (
    account_id         uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    platform_tenant_id uuid        NOT NULL,
    app_id             uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_key       text        NOT NULL,
    window_start       timestamptz NOT NULL,
    request_count      bigint      NOT NULL CHECK (request_count >= 0),
    error_count        bigint      NOT NULL CHECK (error_count >= 0 AND error_count <= request_count),
    billable_units     bigint      NOT NULL CHECK (billable_units >= 0 AND billable_units <= request_count),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, platform_tenant_id, app_id, consumer_key, window_start),
    CONSTRAINT platform_tenant_usage_minutes_tenant_fk
      FOREIGN KEY (account_id, platform_tenant_id)
      REFERENCES platform_tenants(account_id, id) ON DELETE CASCADE,
    CONSTRAINT platform_tenant_usage_minutes_consumer_key_chk
      CHECK (consumer_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT platform_tenant_usage_minutes_minute_chk
      CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);

CREATE INDEX IF NOT EXISTS platform_tenant_usage_minutes_read_idx
  ON platform_tenant_usage_minutes (account_id, platform_tenant_id, window_start, app_id, consumer_key);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS platform_tenant_usage_minutes;
ALTER TABLE api_consumer_usage_events DROP COLUMN IF EXISTS platform_tenant_id;
-- +goose StatementEnd
