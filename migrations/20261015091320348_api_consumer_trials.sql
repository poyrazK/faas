-- filename: 20261015091320348_api_consumer_trials.sql

-- ADR-1314: an API consumer's one free trial. Usage in [starts_at, ends_at)
-- is priced at zero and consumes no included units.

-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_consumer_trials (
    consumer_id uuid PRIMARY KEY REFERENCES api_consumers(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_trials_window_chk CHECK (ends_at > starts_at AND ends_at - starts_at <= interval '365 days'),
    CONSTRAINT api_consumer_trials_minute_chk
      CHECK (starts_at = date_trunc('minute', starts_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
         AND ends_at = date_trunc('minute', ends_at AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')
);
COMMENT ON TABLE api_consumer_trials IS
  'One free trial per API consumer (ADR-1314): usage in [starts_at, ends_at) is priced at zero.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS api_consumer_trials;
-- +goose StatementEnd
