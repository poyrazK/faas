-- filename: 20261021143000512_api_consumer_signups.sql

-- ADR-20261021143000: an app's customers sign up themselves. The settings
-- row turns sign-up on for an app; each sign-up saves a card with Stripe
-- Checkout and, once complete, names the consumer it created and the key it
-- last issued. Gregale keeps only the hash of a sign-up's claim token.
-- ADR-20261021183000: the settings also say whether customers may change
-- plans or close their account with their own key.

-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_consumer_signup_settings (
    app_id uuid PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    enabled boolean NOT NULL DEFAULT true,
    provider text NOT NULL,
    plan_ids uuid[] NOT NULL DEFAULT '{}',
    key_scopes text[] NOT NULL,
    display_name text NOT NULL,
    terms_url text NOT NULL DEFAULT '',
    return_url text NOT NULL DEFAULT '',
    trial_days integer NOT NULL DEFAULT 0,
    daily_limit integer NOT NULL,
    customer_plan_changes boolean NOT NULL DEFAULT false,
    customer_closing boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_signup_settings_provider_chk CHECK (provider = 'stripe'),
    CONSTRAINT api_consumer_signup_settings_plans_chk CHECK (cardinality(plan_ids) <= 20),
    CONSTRAINT api_consumer_signup_settings_scopes_chk CHECK (cardinality(key_scopes) BETWEEN 1 AND 4),
    CONSTRAINT api_consumer_signup_settings_name_chk CHECK (char_length(display_name) BETWEEN 1 AND 80),
    CONSTRAINT api_consumer_signup_settings_urls_chk CHECK (char_length(terms_url) <= 2048 AND char_length(return_url) <= 2048),
    CONSTRAINT api_consumer_signup_settings_trial_chk CHECK (trial_days BETWEEN 0 AND 365),
    CONSTRAINT api_consumer_signup_settings_limit_chk CHECK (daily_limit BETWEEN 1 AND 1000)
);
COMMENT ON TABLE api_consumer_signup_settings IS
  'Self-service sign-up for an app''s API customers (ADR-20261021143000).';

CREATE TABLE IF NOT EXISTS api_consumer_signups (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    email text NOT NULL,
    name text NOT NULL,
    plan_id uuid,
    provider text NOT NULL,
    livemode boolean NOT NULL,
    provider_customer_id text,
    checkout_session_id text,
    claim_hash bytea NOT NULL,
    status text NOT NULL DEFAULT 'pending',
    consumer_id uuid REFERENCES api_consumers(id) ON DELETE SET NULL,
    key_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    completed_at timestamptz,
    CONSTRAINT api_consumer_signups_status_chk CHECK (status IN ('pending', 'completed')),
    CONSTRAINT api_consumer_signups_completed_chk CHECK ((status = 'completed') = (completed_at IS NOT NULL)),
    CONSTRAINT api_consumer_signups_provider_chk CHECK (provider = 'stripe'),
    CONSTRAINT api_consumer_signups_email_chk CHECK (char_length(email) BETWEEN 3 AND 254),
    CONSTRAINT api_consumer_signups_name_chk CHECK (char_length(name) BETWEEN 1 AND 128),
    CONSTRAINT api_consumer_signups_claim_chk CHECK (octet_length(claim_hash) = 32),
    CONSTRAINT api_consumer_signups_expiry_chk CHECK (expires_at > created_at),
    CONSTRAINT api_consumer_signups_provider_ids_chk
      CHECK (char_length(coalesce(provider_customer_id, '')) <= 255 AND char_length(coalesce(checkout_session_id, '')) <= 255)
);
COMMENT ON TABLE api_consumer_signups IS
  'An API customer''s self-service sign-up (ADR-20261021143000); pending rows are deleted a week after they expire.';
CREATE INDEX IF NOT EXISTS api_consumer_signups_app_created_idx ON api_consumer_signups (app_id, created_at DESC);
CREATE INDEX IF NOT EXISTS api_consumer_signups_pending_expiry_idx ON api_consumer_signups (expires_at) WHERE status = 'pending';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_signups;
DROP TABLE IF EXISTS api_consumer_signup_settings;
-- +goose StatementEnd
