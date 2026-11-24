-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE api_consumer_credit_pack_settings (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    return_url text NOT NULL,
    packs jsonb NOT NULL CHECK (jsonb_typeof(packs) = 'array')
);

CREATE TABLE api_consumer_credit_purchases (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    pack_id text NOT NULL,
    label text NOT NULL,
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    amount_millicents bigint NOT NULL CHECK (amount_millicents BETWEEN 1 AND 1000000000000),
    amount_minor bigint NOT NULL CHECK (amount_minor > 0),
    return_url text NOT NULL,
    stripe_customer text NOT NULL,
    stripe_session text UNIQUE,
    livemode boolean NOT NULL,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'credited', 'expired')),
    expires_at timestamptz NOT NULL,
    next_check_at timestamptz NOT NULL,
    CHECK (status <> 'credited' OR stripe_session IS NOT NULL)
);

CREATE UNIQUE INDEX api_consumer_credit_purchases_pending_consumer
    ON api_consumer_credit_purchases (consumer_id) WHERE status = 'pending';
CREATE INDEX api_consumer_credit_purchases_due
    ON api_consumer_credit_purchases (next_check_at, id) WHERE status = 'pending';
CREATE INDEX api_consumer_credit_purchases_pending_live_account
    ON api_consumer_credit_purchases (account_id) WHERE status = 'pending' AND livemode;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE api_consumer_credit_purchases;
DROP TABLE api_consumer_credit_pack_settings;
