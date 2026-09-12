-- filename: 20260911215708144_api_consumer_rate_cards.sql

-- +goose Up
-- +goose StatementBegin
-- Immutable, versioned customer pricing for API consumer request usage.
-- A new row supersedes older rows at effective_from; existing rows are never
-- updated so a historical quote can always explain which price was applied.
CREATE TABLE IF NOT EXISTS api_consumer_rate_cards (
    id                          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id                  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id                      uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    currency                    text NOT NULL,
    unit                        text NOT NULL DEFAULT 'request',
    price_millicents_per_unit   bigint NOT NULL,
    effective_from              timestamptz NOT NULL,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_rate_cards_currency_chk
      CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT api_consumer_rate_cards_unit_chk
      CHECK (unit = 'request'),
    CONSTRAINT api_consumer_rate_cards_price_chk
      CHECK (price_millicents_per_unit >= 0),
    CONSTRAINT api_consumer_rate_cards_effective_minute_chk
      CHECK (effective_from = date_trunc('minute', effective_from)),
    CONSTRAINT api_consumer_rate_cards_app_effective_uniq
      UNIQUE (app_id, effective_from)
);

CREATE INDEX IF NOT EXISTS api_consumer_rate_cards_account_app_effective_idx
  ON api_consumer_rate_cards (account_id, app_id, effective_from ASC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS api_consumer_rate_cards_account_app_effective_idx;
DROP TABLE IF EXISTS api_consumer_rate_cards;
-- +goose StatementEnd
