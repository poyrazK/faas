-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE account_billing_connections ADD COLUMN IF NOT EXISTS stripe_account_id text;
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_constraint
        WHERE conrelid = 'account_billing_connections'::regclass
          AND conname = 'account_billing_connections_stripe_account_chk'
    ) THEN
        ALTER TABLE account_billing_connections ADD CONSTRAINT account_billing_connections_stripe_account_chk
            CHECK (stripe_account_id IS NULL OR (provider = 'stripe' AND stripe_account_id ~ '^acct_[A-Za-z0-9]+$')) NOT VALID;
    END IF;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS api_consumer_stripe_links (
    consumer_id uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    stripe_account_id text NOT NULL,
    livemode boolean NOT NULL,
    stripe_customer_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer_id, stripe_account_id, livemode),
    CONSTRAINT api_consumer_stripe_links_account_chk CHECK (stripe_account_id ~ '^acct_[A-Za-z0-9]+$'),
    CONSTRAINT api_consumer_stripe_links_customer_chk CHECK (stripe_customer_id ~ '^cus_[A-Za-z0-9]+$')
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE api_consumer_stripe_links;
ALTER TABLE account_billing_connections DROP COLUMN stripe_account_id;
