-- filename: 20261009181204317_api_consumer_plans.sql

-- ADR-847: named consumer plans. A plan bundles enforcement limits with its
-- own rate-card history; app-wide cards (plan_id NULL) remain the default
-- plan, so existing apps and statements are unchanged. Consumers move between
-- plans through append-only, minute-effective assignments, and the gateway
-- admits plan-limited requests against one counter row per consumer.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_consumer_plans (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id              uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id                  uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    name                    text NOT NULL,
    max_requests_per_minute bigint NOT NULL DEFAULT 0,
    max_units_per_month     bigint NOT NULL DEFAULT 0,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_plans_name_chk CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    CONSTRAINT api_consumer_plans_limits_chk CHECK (max_requests_per_minute >= 0 AND max_units_per_month >= 0),
    CONSTRAINT api_consumer_plans_app_name_uniq UNIQUE (app_id, name),
    CONSTRAINT api_consumer_plans_app_id_uniq UNIQUE (app_id, id)
);

ALTER TABLE api_consumer_rate_cards
  ADD COLUMN IF NOT EXISTS plan_id uuid;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_consumer_rate_cards_plan_fkey') THEN
    ALTER TABLE api_consumer_rate_cards
      ADD CONSTRAINT api_consumer_rate_cards_plan_fkey
        FOREIGN KEY (app_id, plan_id) REFERENCES api_consumer_plans(app_id, id) ON DELETE CASCADE;
  END IF;
END $$;
-- Each price history (the app default or one plan) has unique minutes.
ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_app_effective_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_rate_cards_default_effective_uniq
  ON api_consumer_rate_cards (app_id, effective_from) WHERE plan_id IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_rate_cards_plan_effective_uniq
  ON api_consumer_rate_cards (plan_id, effective_from) WHERE plan_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS api_consumer_plan_assignments (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id     uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id         uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id    uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    plan_id        uuid,
    effective_from timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_plan_assignments_plan_fkey
      FOREIGN KEY (app_id, plan_id) REFERENCES api_consumer_plans(app_id, id) ON DELETE CASCADE,
    CONSTRAINT api_consumer_plan_assignments_minute_chk
      CHECK (effective_from = date_trunc('minute', effective_from)),
    CONSTRAINT api_consumer_plan_assignments_consumer_effective_uniq UNIQUE (consumer_id, effective_from)
);
CREATE INDEX IF NOT EXISTS api_consumer_plan_assignments_lookup_idx
  ON api_consumer_plan_assignments (account_id, app_id, consumer_id, effective_from DESC);

CREATE TABLE IF NOT EXISTS api_consumer_plan_admissions (
    consumer_id  uuid PRIMARY KEY REFERENCES api_consumers(id) ON DELETE CASCADE,
    account_id   uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    minute_start timestamptz NOT NULL,
    minute_used  bigint NOT NULL DEFAULT 0,
    month_start  timestamptz NOT NULL,
    month_used   bigint NOT NULL DEFAULT 0,
    CONSTRAINT api_consumer_plan_admissions_used_chk CHECK (minute_used >= 0 AND month_used >= 0)
);

COMMENT ON TABLE api_consumer_plans IS
  'Named consumer plans: enforcement limits plus their own rate-card history (api_consumer_rate_cards.plan_id).';
COMMENT ON COLUMN api_consumer_rate_cards.plan_id IS
  'Plan whose price history this card belongs to; NULL is the app default plan.';
COMMENT ON TABLE api_consumer_plan_assignments IS
  'Append-only, minute-effective plan assignments; a NULL plan_id returns the consumer to the default plan.';
COMMENT ON TABLE api_consumer_plan_admissions IS
  'Cross-replica admission counters for plan limits: requests this minute and weighted units this UTC month.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_plan_admissions;
DROP TABLE IF EXISTS api_consumer_plan_assignments;
DELETE FROM api_consumer_rate_cards WHERE plan_id IS NOT NULL;
DROP INDEX IF EXISTS api_consumer_rate_cards_plan_effective_uniq;
DROP INDEX IF EXISTS api_consumer_rate_cards_default_effective_uniq;
ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_plan_fkey;
ALTER TABLE api_consumer_rate_cards
  DROP COLUMN IF EXISTS plan_id;
ALTER TABLE api_consumer_rate_cards
  ADD CONSTRAINT api_consumer_rate_cards_app_effective_uniq UNIQUE (app_id, effective_from);
DROP TABLE IF EXISTS api_consumer_plans;
-- +goose StatementEnd
