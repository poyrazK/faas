-- filename: 20260923220000137_account_release_webhook_scope.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-224: an account may configure one release receiver for all its apps.
-- Existing rows remain app-scoped; account-scoped rows do not bind to an app.
-- Delivery rows still identify the source app and use the same dispatcher.
ALTER TABLE app_webhooks
    ADD COLUMN IF NOT EXISTS scope text NOT NULL DEFAULT 'app';
ALTER TABLE app_webhooks
    ALTER COLUMN app_id DROP NOT NULL;

-- The account scope is deliberately narrower than the app scope: it cannot
-- subscribe to every present or future platform event by using an empty
-- filter. Public writes and fan-out land in later PRs.
ALTER TABLE app_webhooks
    DROP CONSTRAINT IF EXISTS app_webhooks_scope_chk;
ALTER TABLE app_webhooks
    ADD CONSTRAINT app_webhooks_scope_chk CHECK (
        (scope = 'app' AND app_id IS NOT NULL)
        OR (scope = 'account' AND app_id IS NULL
            AND cardinality(event_filter) BETWEEN 1 AND 4
            AND array_position(event_filter, NULL::text) IS NULL
            AND event_filter <@ ARRAY[
                'deployment.live', 'deployment.failed',
                'rollout.completed', 'rollout.aborted'
            ]::text[])
    ) NOT VALID;
ALTER TABLE app_webhooks VALIDATE CONSTRAINT app_webhooks_scope_chk;

-- PostgreSQL's existing (app_id, target_url) unique index permits repeated
-- NULL app IDs. Give the account scope its own owner-qualified uniqueness.
CREATE UNIQUE INDEX IF NOT EXISTS app_webhooks_account_target_uniq
    ON app_webhooks (account_id, target_url)
    WHERE scope = 'account';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Forward-only: removing this scope after account receivers are created could
-- strand subscriptions and their durable delivery history.
SELECT 1;
-- +goose StatementEnd
