-- Customer-owned PostgreSQL schema. Install explicitly as the database owner.
-- Receipt and business writes commit together. Retain receipts while deliveries
-- or historical replays can occur; deleting a receipt permits effects to repeat.
CREATE TABLE IF NOT EXISTS public.gregale_event_consumer_inbox (
    identity_digest bytea PRIMARY KEY CHECK (octet_length(identity_digest) = 32),
    account_id uuid NOT NULL,
    app_id uuid NOT NULL,
    subscription_id uuid NOT NULL,
    acceptance_id bigint NOT NULL CHECK (acceptance_id > 0),
    event_source text NOT NULL,
    event_id text NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    response_body text NOT NULL CHECK (
        octet_length(response_body) BETWEEN 1 AND 1048576
        AND response_body::jsonb IS NOT NULL
    ),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (account_id, app_id, subscription_id, acceptance_id)
);
