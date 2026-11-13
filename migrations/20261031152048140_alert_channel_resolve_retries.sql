-- filename: 20261031152048140_alert_channel_resolve_retries.sql
-- migration-compat: expand
-- ADR-20261031144636: a recovery notice a notification channel did not take
-- is retried with backoff for up to a day, so a PagerDuty incident opened by
-- a fire does not stay open because the resolve met a short outage. One row
-- per rule and channel; a new fire of the rule clears its rows.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS alert_channel_resolve_retries (
    rule_id         uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    channel_id      uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    observed        double precision NOT NULL,
    resolved_at     timestamptz NOT NULL,
    attempts        integer NOT NULL CHECK (attempts > 0),
    next_attempt_at timestamptz NOT NULL,
    last_error      text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 512),
    PRIMARY KEY (rule_id, channel_id)
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS alert_channel_resolve_retries_due_idx ON alert_channel_resolve_retries (next_attempt_at);
CREATE INDEX IF NOT EXISTS alert_channel_resolve_retries_channel_idx ON alert_channel_resolve_retries (channel_id);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS alert_channel_resolve_retries;
