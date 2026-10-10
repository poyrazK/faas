-- filename: 20261009172036793_api_consumer_route_weights.sql

-- ADR-846: rate cards can weight routes. The gateway labels each
-- consumer-attributed request with a bounded "METHOD /template" route, and
-- apid keeps billable units per route and minute next to the per-minute
-- totals, which stay authoritative. Cards without weights are unchanged.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_consumer_route_usage_minutes (
    account_id     uuid        NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id         uuid        NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_key   text        NOT NULL,
    route          text        NOT NULL,
    window_start   timestamptz NOT NULL,
    billable_units bigint      NOT NULL DEFAULT 0,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, app_id, consumer_key, window_start, route),
    CONSTRAINT api_consumer_route_usage_minutes_consumer_key_chk
        CHECK (consumer_key ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'),
    CONSTRAINT api_consumer_route_usage_minutes_route_chk
        CHECK (char_length(route) BETWEEN 3 AND 256),
    CONSTRAINT api_consumer_route_usage_minutes_units_chk
        CHECK (billable_units >= 0),
    CONSTRAINT api_consumer_route_usage_minutes_minute_chk
        CHECK (window_start = date_trunc('minute', window_start AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);

ALTER TABLE api_consumer_rate_cards
  ADD COLUMN IF NOT EXISTS route_weights jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_route_weights_chk;
ALTER TABLE api_consumer_rate_cards
  ADD CONSTRAINT api_consumer_rate_cards_route_weights_chk
    CHECK (jsonb_typeof(route_weights) = 'object');

COMMENT ON TABLE api_consumer_route_usage_minutes IS
  'Billable units per consumer, bounded route label, and UTC minute; per-minute totals stay in api_consumer_usage_minutes.';
COMMENT ON COLUMN api_consumer_rate_cards.route_weights IS
  'Units charged per request on a route label ("METHOD /template" -> weight); unlisted routes count 1.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE api_consumer_rate_cards
  DROP CONSTRAINT IF EXISTS api_consumer_rate_cards_route_weights_chk;
ALTER TABLE api_consumer_rate_cards
  DROP COLUMN IF EXISTS route_weights;
DROP TABLE IF EXISTS api_consumer_route_usage_minutes;
-- +goose StatementEnd
