-- filename: 20261011154457338_outbound_ai_cost_and_app_limits.sql
-- ADR-983: per-app AI token limits, customer price lists, cost budgets and
-- AI fallback routes. Additive and replay-safe.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE outbound_app_bindings
    ADD COLUMN IF NOT EXISTS ai_daily_token_limit bigint;
ALTER TABLE outbound_app_binding_usage
    ADD COLUMN IF NOT EXISTS daily_ai_token_count bigint NOT NULL DEFAULT 0;
ALTER TABLE outbound_integrations
    ADD COLUMN IF NOT EXISTS ai_daily_cost_limit_millicents bigint,
    ADD COLUMN IF NOT EXISTS ai_monthly_cost_limit_millicents bigint;
ALTER TABLE outbound_admission_state
    ADD COLUMN IF NOT EXISTS daily_ai_cost_millicents bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS monthly_usage_month date NOT NULL DEFAULT (date_trunc('month', now() AT TIME ZONE 'UTC'))::date,
    ADD COLUMN IF NOT EXISTS monthly_ai_cost_millicents bigint NOT NULL DEFAULT 0;
ALTER TABLE outbound_ai_usage_daily
    ADD COLUMN IF NOT EXISTS cost_millicents bigint NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_app_bindings_ai_daily_token_limit_chk' AND conrelid = 'outbound_app_bindings'::regclass) THEN
        ALTER TABLE outbound_app_bindings ADD CONSTRAINT outbound_app_bindings_ai_daily_token_limit_chk
            CHECK (ai_daily_token_limit IS NULL OR ai_daily_token_limit BETWEEN 1 AND 100000000000);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_app_binding_usage_daily_ai_token_count_chk' AND conrelid = 'outbound_app_binding_usage'::regclass) THEN
        ALTER TABLE outbound_app_binding_usage ADD CONSTRAINT outbound_app_binding_usage_daily_ai_token_count_chk
            CHECK (daily_ai_token_count >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_integrations_ai_cost_limits_chk' AND conrelid = 'outbound_integrations'::regclass) THEN
        ALTER TABLE outbound_integrations ADD CONSTRAINT outbound_integrations_ai_cost_limits_chk
            CHECK ((ai_daily_cost_limit_millicents IS NULL AND ai_monthly_cost_limit_millicents IS NULL)
                   OR (ai_provider IS NOT NULL
                       AND (ai_daily_cost_limit_millicents IS NULL OR ai_daily_cost_limit_millicents BETWEEN 1 AND 1000000000000)
                       AND (ai_monthly_cost_limit_millicents IS NULL OR ai_monthly_cost_limit_millicents BETWEEN 1 AND 1000000000000)));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_admission_state_ai_cost_chk' AND conrelid = 'outbound_admission_state'::regclass) THEN
        ALTER TABLE outbound_admission_state ADD CONSTRAINT outbound_admission_state_ai_cost_chk
            CHECK (daily_ai_cost_millicents >= 0 AND monthly_ai_cost_millicents >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_usage_daily_cost_chk' AND conrelid = 'outbound_ai_usage_daily'::regclass) THEN
        ALTER TABLE outbound_ai_usage_daily ADD CONSTRAINT outbound_ai_usage_daily_cost_chk
            CHECK (cost_millicents >= 0);
    END IF;
END$$;

-- A price list is immutable once written; replacing prices inserts a new
-- list. The current list is the newest one whose effective_from has passed.
CREATE TABLE IF NOT EXISTS outbound_ai_price_lists (
    id             uuid PRIMARY KEY,
    integration_id uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    effective_from timestamptz NOT NULL DEFAULT now(),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS outbound_ai_price_lists_current_idx
    ON outbound_ai_price_lists (integration_id, effective_from DESC);

-- Prices are millicents per million tokens. Null cached or cache-write
-- prices fall back to the input price.
CREATE TABLE IF NOT EXISTS outbound_ai_price_entries (
    price_list_id                    uuid NOT NULL REFERENCES outbound_ai_price_lists (id) ON DELETE CASCADE,
    model_prefix                     text NOT NULL,
    input_millicents_per_mtok        bigint NOT NULL,
    cached_input_millicents_per_mtok bigint,
    cache_write_millicents_per_mtok  bigint,
    output_millicents_per_mtok       bigint NOT NULL,
    PRIMARY KEY (price_list_id, model_prefix),
    CONSTRAINT outbound_ai_price_entries_prefix_chk
        CHECK (length(model_prefix) BETWEEN 1 AND 128 AND model_prefix ~ '^[ -~]+$'),
    CONSTRAINT outbound_ai_price_entries_prices_chk
        CHECK (input_millicents_per_mtok BETWEEN 0 AND 1000000000
               AND output_millicents_per_mtok BETWEEN 0 AND 1000000000
               AND (cached_input_millicents_per_mtok IS NULL OR cached_input_millicents_per_mtok BETWEEN 0 AND 1000000000)
               AND (cache_write_millicents_per_mtok IS NULL OR cache_write_millicents_per_mtok BETWEEN 0 AND 1000000000))
);

-- AI fallback routes: an ordered list of same-format managed integrations.
CREATE TABLE IF NOT EXISTS outbound_ai_routes (
    id         uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT outbound_ai_routes_name_chk CHECK (name ~ '^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$'),
    CONSTRAINT outbound_ai_routes_account_name_key UNIQUE (account_id, name)
);

CREATE TABLE IF NOT EXISTS outbound_ai_route_targets (
    route_id       uuid NOT NULL REFERENCES outbound_ai_routes (id) ON DELETE CASCADE,
    position       smallint NOT NULL,
    integration_id uuid NOT NULL REFERENCES outbound_integrations (id) ON DELETE CASCADE,
    model          text,
    PRIMARY KEY (route_id, position),
    CONSTRAINT outbound_ai_route_targets_position_chk CHECK (position BETWEEN 1 AND 5),
    CONSTRAINT outbound_ai_route_targets_model_chk
        CHECK (model IS NULL OR (length(model) BETWEEN 1 AND 128 AND model ~ '^[ -~]+$'))
);
CREATE INDEX IF NOT EXISTS outbound_ai_route_targets_integration_idx
    ON outbound_ai_route_targets (integration_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS outbound_ai_route_targets;
DROP TABLE IF EXISTS outbound_ai_routes;
DROP TABLE IF EXISTS outbound_ai_price_entries;
DROP TABLE IF EXISTS outbound_ai_price_lists;
ALTER TABLE outbound_ai_usage_daily
    DROP CONSTRAINT IF EXISTS outbound_ai_usage_daily_cost_chk,
    DROP COLUMN IF EXISTS cost_millicents;
ALTER TABLE outbound_admission_state
    DROP CONSTRAINT IF EXISTS outbound_admission_state_ai_cost_chk,
    DROP COLUMN IF EXISTS monthly_ai_cost_millicents,
    DROP COLUMN IF EXISTS monthly_usage_month,
    DROP COLUMN IF EXISTS daily_ai_cost_millicents;
ALTER TABLE outbound_integrations
    DROP CONSTRAINT IF EXISTS outbound_integrations_ai_cost_limits_chk,
    DROP COLUMN IF EXISTS ai_monthly_cost_limit_millicents,
    DROP COLUMN IF EXISTS ai_daily_cost_limit_millicents;
ALTER TABLE outbound_app_binding_usage
    DROP CONSTRAINT IF EXISTS outbound_app_binding_usage_daily_ai_token_count_chk,
    DROP COLUMN IF EXISTS daily_ai_token_count;
ALTER TABLE outbound_app_bindings
    DROP CONSTRAINT IF EXISTS outbound_app_bindings_ai_daily_token_limit_chk,
    DROP COLUMN IF EXISTS ai_daily_token_limit;
-- +goose StatementEnd
