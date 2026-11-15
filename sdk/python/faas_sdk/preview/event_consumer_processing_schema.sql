-- Application-owned PostgreSQL DDL. Install alongside the consumer inbox.
-- Successful completion deletes the lease with the business/receipt transaction.
CREATE TABLE IF NOT EXISTS public.gregale_event_consumer_processing (
    identity_digest bytea PRIMARY KEY CHECK (octet_length(identity_digest) = 32),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    claim_token bytea NOT NULL CHECK (octet_length(claim_token) = 32),
    expires_at timestamptz NOT NULL
);
