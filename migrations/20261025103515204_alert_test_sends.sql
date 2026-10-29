-- filename: 20261025103515204_alert_test_sends.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Test sends relayed to meterd (ADR 20261022111812, section 3). apid records
-- a request to test a notification channel or an alert rule's webhook;
-- meterd, which holds the alerting key, opens the destination, sends, and
-- writes the result back. apid waits for it and then deletes the row.
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS alert_test_sends (
    id            uuid PRIMARY KEY,
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('notification_channel', 'alert_rule_webhook')),
    subject_id    uuid NOT NULL,
    event         jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (pg_column_size(event) <= 65536),
    state         text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'claimed', 'done')),
    claim_token   uuid,
    claimed_until timestamptz,
    result        jsonb CHECK (result IS NULL OR pg_column_size(result) <= 8192),
    created_at    timestamptz NOT NULL DEFAULT now(),
    completed_at  timestamptz,
    CHECK ((state = 'claimed') = (claim_token IS NOT NULL AND claimed_until IS NOT NULL)),
    CHECK ((state = 'done') = (result IS NOT NULL AND completed_at IS NOT NULL))
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS alert_test_sends_open ON alert_test_sends (created_at) WHERE state <> 'done';

COMMENT ON TABLE alert_test_sends IS
    'Test sends apid relays to meterd, which holds the alerting seal-domain key (ADR 20261022111812). Short-lived: apid deletes a row once it reads the result, meterd prunes abandoned ones.';

-- +goose Down
DROP TABLE IF EXISTS alert_test_sends;
