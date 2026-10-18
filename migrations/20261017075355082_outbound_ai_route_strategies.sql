-- filename: 20261017075355082_outbound_ai_route_strategies.sql
-- ADR-1434: an AI route has a strategy that picks the target a call tries
-- first (ordered, weighted, latency or cost), targets may carry a weight,
-- and a cost route may name a latency target. Additive and replay-safe.

-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
ALTER TABLE outbound_ai_routes
    ADD COLUMN IF NOT EXISTS strategy text NOT NULL DEFAULT 'ordered',
    ADD COLUMN IF NOT EXISTS max_ttfb_ms integer;
ALTER TABLE outbound_ai_route_targets
    ADD COLUMN IF NOT EXISTS weight smallint;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_routes_strategy_chk' AND conrelid = 'outbound_ai_routes'::regclass) THEN
        ALTER TABLE outbound_ai_routes ADD CONSTRAINT outbound_ai_routes_strategy_chk
            CHECK (strategy IN ('ordered', 'weighted', 'latency', 'cost')
                   AND (max_ttfb_ms IS NULL OR (strategy = 'cost' AND max_ttfb_ms BETWEEN 1 AND 600000)));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint
                   WHERE conname = 'outbound_ai_route_targets_weight_chk' AND conrelid = 'outbound_ai_route_targets'::regclass) THEN
        ALTER TABLE outbound_ai_route_targets ADD CONSTRAINT outbound_ai_route_targets_weight_chk
            CHECK (weight IS NULL OR weight BETWEEN 0 AND 100);
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE outbound_ai_route_targets
    DROP CONSTRAINT IF EXISTS outbound_ai_route_targets_weight_chk,
    DROP COLUMN IF EXISTS weight;
ALTER TABLE outbound_ai_routes
    DROP CONSTRAINT IF EXISTS outbound_ai_routes_strategy_chk,
    DROP COLUMN IF EXISTS max_ttfb_ms,
    DROP COLUMN IF EXISTS strategy;
-- +goose StatementEnd
