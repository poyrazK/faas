-- migration-compat: expand
-- filename: 20261124213833200_mcp_oauth_consumer_identities.sql

-- ADR-20261124094745: a caller signed in through an MCP server's
-- oauth-proxy issuer becomes an API consumer of that app, identified by the
-- token's issuer and subject. Older apid and gateway builds ignore the table.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS api_consumer_identities (
    id          uuid NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id      uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    consumer_id uuid NOT NULL REFERENCES api_consumers(id) ON DELETE CASCADE,
    issuer      text NOT NULL,
    subject     text NOT NULL,
    email       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_consumer_identities_issuer_chk CHECK (char_length(issuer) BETWEEN 1 AND 2048),
    CONSTRAINT api_consumer_identities_subject_chk CHECK (char_length(subject) BETWEEN 1 AND 255),
    CONSTRAINT api_consumer_identities_email_chk CHECK (char_length(email) <= 254)
);
CREATE UNIQUE INDEX IF NOT EXISTS api_consumer_identities_app_subject_uniq
  ON api_consumer_identities (app_id, issuer, subject);
CREATE INDEX IF NOT EXISTS api_consumer_identities_consumer_idx
  ON api_consumer_identities (consumer_id);
CREATE INDEX IF NOT EXISTS api_consumer_identities_app_created_idx
  ON api_consumer_identities (app_id, created_at);

COMMENT ON TABLE api_consumer_identities IS
  'OAuth identities (issuer + subject) of an app''s API consumers: MCP oauth-proxy sign-ins enroll as consumers (ADR-20261124094745).';

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS api_consumer_identities;
