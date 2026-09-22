-- filename: 20260922152914063_inbound_webhook_inbox.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-212: public, provider-verified webhook ingress that acknowledges only
-- after a durable invocation row exists. The endpoint token is a bearer
-- capability used only for routing; its SHA-256 digest is the only form stored.
-- Provider signing secrets use the same age/X25519-at-rest posture as outbound
-- app-webhook secrets and are never returned after create/update.
CREATE TABLE IF NOT EXISTS inbound_webhook_endpoints (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    app_id                uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    account_id            uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name                  text NOT NULL,
    provider              text NOT NULL,
    token_hash            bytea NOT NULL,
    signing_secret_sealed bytea NOT NULL,
    delivery_path         text NOT NULL DEFAULT '/',
    enabled               boolean NOT NULL DEFAULT true,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT inbound_webhook_endpoints_provider_chk CHECK (
        provider IN ('stripe')
    ),
    CONSTRAINT inbound_webhook_endpoints_name_chk CHECK (
        name ~ '^[a-z][a-z0-9-]{0,62}$'
    ),
    CONSTRAINT inbound_webhook_endpoints_delivery_path_chk CHECK (
        char_length(delivery_path) BETWEEN 1 AND 256
        AND left(delivery_path, 1) = '/'
        AND position('?' IN delivery_path) = 0
        AND position('#' IN delivery_path) = 0
    ),
    CONSTRAINT inbound_webhook_endpoints_token_hash_len_chk CHECK (
        octet_length(token_hash) = 32
    ),
    CONSTRAINT inbound_webhook_endpoints_app_name_uniq UNIQUE (app_id, name),
    CONSTRAINT inbound_webhook_endpoints_token_hash_uniq UNIQUE (token_hash)
);

CREATE INDEX IF NOT EXISTS inbound_webhook_endpoints_account_idx
    ON inbound_webhook_endpoints (account_id);
CREATE INDEX IF NOT EXISTS inbound_webhook_endpoints_app_created_idx
    ON inbound_webhook_endpoints (app_id, created_at, id);

ALTER TABLE invocations DROP CONSTRAINT invocations_source_check;
ALTER TABLE invocations ADD CONSTRAINT invocations_source_check
    CHECK (source IN (
        'async_invoke', 'inbound_webhook', 'queue', 'delayed_task',
        'cron', 'replay', 'esm'
    ));

-- Keep failed-invocation alerting complete when the new invocation source is
-- introduced. Both the explicit source filter and the evaluator's "any"
-- aggregate include inbound webhook delivery failures.
ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_failure_source_chk;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_failure_source_chk CHECK (
    failure_source IS NULL OR failure_source IN (
        'any', 'cron', 'queue', 'delayed_task', 'async_invoke',
        'inbound_webhook'
    )
);
-- +goose StatementEnd

-- +goose Down
-- Forward-only: endpoint deletion would strand provider configurations and
-- make already-issued ingress URLs silently stop resolving during rollback.
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
