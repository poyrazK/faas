-- filename: 20261104142825397_alert_channel_stand_downs.sql
-- migration-compat: expand
-- ADR-20261104142830: an alert episode that ends without its rule
-- recovering still owes each notification channel a recovery notice, or a
-- PagerDuty incident the fire opened stays open for ever. The rule may be
-- disabled, no longer evaluated (its plan, flags or account stopped
-- covering it, or its app was deleted), or deleted outright. One row per
-- rule and channel carries what the notice needs, so it survives the rule:
-- rule_id has no foreign key. A BEFORE DELETE trigger queues the notices of
-- a deleted rule on every delete path (directly, or through its SLO,
-- synthetic check or app). It is named to run before
-- alert_rules_keep_episodes, which resolves the open episode it looks for.
-- Without an account (the delete is part of deleting the account) nothing is
-- queued.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS alert_channel_stand_downs (
    rule_id         uuid NOT NULL,
    channel_id      uuid NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    account_id      uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id          uuid,
    rule_name       text NOT NULL,
    rule_metric     text NOT NULL,
    reason          text NOT NULL CHECK (reason IN ('disabled', 'not_released', 'app_deleted', 'deleted')),
    queued_at       timestamptz NOT NULL,
    attempts        integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL,
    last_error      text NOT NULL DEFAULT '' CHECK (char_length(last_error) <= 512),
    PRIMARY KEY (rule_id, channel_id)
);
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS alert_channel_stand_downs_due_idx ON alert_channel_stand_downs (next_attempt_at);
CREATE INDEX IF NOT EXISTS alert_channel_stand_downs_channel_idx ON alert_channel_stand_downs (channel_id);
CREATE INDEX IF NOT EXISTS alert_channel_stand_downs_account_idx ON alert_channel_stand_downs (account_id);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION alert_rules_queue_channel_stand_downs() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    INSERT INTO alert_channel_stand_downs
        (rule_id, channel_id, account_id, app_id, rule_name, rule_metric, reason, queued_at, next_attempt_at)
    SELECT OLD.id, owed.channel_id, OLD.account_id, OLD.app_id, OLD.name, OLD.metric, 'deleted', now(), now()
      FROM (
            SELECT b.channel_id FROM alert_rule_channels b
             WHERE b.rule_id = OLD.id
               AND EXISTS (SELECT 1 FROM alert_episodes e WHERE e.rule_id = OLD.id AND e.resolved_at IS NULL)
            UNION
            SELECT r.channel_id FROM alert_channel_resolve_retries r WHERE r.rule_id = OLD.id
           ) owed
      JOIN notification_channels c ON c.id = owed.channel_id
      JOIN accounts a ON a.id = OLD.account_id
    ON CONFLICT (rule_id, channel_id) DO NOTHING;
    RETURN OLD;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS alert_rules_channel_stand_downs ON alert_rules;
CREATE TRIGGER alert_rules_channel_stand_downs BEFORE DELETE ON alert_rules
    FOR EACH ROW EXECUTE FUNCTION alert_rules_queue_channel_stand_downs();

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER IF EXISTS alert_rules_channel_stand_downs ON alert_rules;
DROP FUNCTION IF EXISTS alert_rules_queue_channel_stand_downs();
DROP TABLE IF EXISTS alert_channel_stand_downs;
