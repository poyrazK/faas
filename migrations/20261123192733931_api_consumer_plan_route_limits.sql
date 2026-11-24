-- migration-compat: expand
-- filename: 20261123192733931_api_consumer_plan_route_limits.sql

-- ADR-20261123192254: consumer plans can cap single billing routes, such as
-- one MCP tool ("MCP /tools/<name>"), per consumer per minute and per UTC
-- month. Limits live on the plan as a JSON object keyed by route label; the
-- gateway admits a limited route against one counter row per consumer and
-- route, locked after the consumer's plan admission row. Older apid and
-- gateway builds ignore the column and the table.

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_consumer_plans
  ADD COLUMN IF NOT EXISTS route_limits jsonb NOT NULL DEFAULT '{}'::jsonb;

-- +goose StatementBegin
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_plans_route_limits_chk') THEN
    -- Every existing row holds the '{}' default; new writes are checked at once.
    ALTER TABLE api_consumer_plans
      ADD CONSTRAINT api_consumer_plans_route_limits_chk
        CHECK (jsonb_typeof(route_limits) = 'object') NOT VALID;
  END IF;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS api_consumer_plan_route_admissions (
    consumer_id  uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    route        text NOT NULL,
    minute_start timestamptz NOT NULL,
    minute_used  bigint NOT NULL DEFAULT 0,
    month_start  timestamptz NOT NULL,
    month_used   bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (consumer_id, route),
    CONSTRAINT api_consumer_plan_route_admissions_route_chk CHECK (char_length(route) BETWEEN 3 AND 256),
    CONSTRAINT api_consumer_plan_route_admissions_used_chk CHECK (minute_used >= 0 AND month_used >= 0)
);
CREATE INDEX IF NOT EXISTS api_consumer_plan_route_admissions_account_idx
  ON api_consumer_plan_route_admissions (account_id);

COMMENT ON COLUMN api_consumer_plans.route_limits IS
  'Per-route caps keyed by billing route label: {"MCP /tools/search": {"max_requests_per_minute": n, "max_requests_per_month": n}}.';
COMMENT ON TABLE api_consumer_plan_route_admissions IS
  'Cross-replica admission counters for plan route limits: requests on one route this minute and this UTC month.';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS api_consumer_plan_route_admissions;
ALTER TABLE api_consumer_plans
  DROP CONSTRAINT IF EXISTS api_consumer_plans_route_limits_chk;
ALTER TABLE api_consumer_plans
  DROP COLUMN IF EXISTS route_limits;
