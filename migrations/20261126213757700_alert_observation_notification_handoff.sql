-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE alert_channel_stand_downs
    ADD COLUMN IF NOT EXISTS observation_transition boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS pagerduty_target_sealed bytea,
    ADD COLUMN IF NOT EXISTS pagerduty_region text;

CREATE TABLE IF NOT EXISTS alert_webhook_stand_downs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    app_id uuid,
    rule_name text NOT NULL,
    metric text NOT NULL,
    comparison text NOT NULL,
    threshold double precision NOT NULL,
    window_spec text NOT NULL,
    environment_scope text NOT NULL,
    webhook_url text NOT NULL,
    webhook_secret_sealed bytea NOT NULL,
    queued_at timestamptz NOT NULL,
    next_attempt_at timestamptz NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0)
);
CREATE INDEX IF NOT EXISTS alert_webhook_stand_downs_rule_idx ON alert_webhook_stand_downs (rule_id);
CREATE INDEX IF NOT EXISTS alert_webhook_stand_downs_due_idx ON alert_webhook_stand_downs (next_attempt_at);

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON alert_webhook_stand_downs TO faas_runtime;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE alert_webhook_stand_downs;
ALTER TABLE alert_channel_stand_downs DROP COLUMN pagerduty_region,
    DROP COLUMN pagerduty_target_sealed,
    DROP COLUMN observation_transition;
