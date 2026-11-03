-- migration-compat: expand
-- filename: 20261101030512417_deployment_event_subscription_intents.sql
--
-- ADR-20261101030512: a production manifest deploy records the event
-- subscriptions its gregale.yaml declares for the app. apid applies that set
-- (subscriptions it does not declare are disabled) only once the deployment
-- serves all production traffic, under the same rules as staged crons; a
-- dark, failed, cancelled or superseded deployment never applies it. One row
-- per deployment; apid is the only writer. A new table; the previous release
-- never reads it.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS deployment_event_subscription_intents (
    deployment_id uuid PRIMARY KEY REFERENCES deployments(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    declared_subscription_ids uuid[] NOT NULL,
    status text NOT NULL DEFAULT 'prepared'
        CHECK (status IN ('prepared', 'activated', 'discarded')),
    reason text NOT NULL DEFAULT '' CHECK (char_length(reason) <= 64),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS deployment_event_subscription_intents_prepared_idx
    ON deployment_event_subscription_intents (updated_at, deployment_id)
    WHERE status = 'prepared';
CREATE INDEX IF NOT EXISTS deployment_event_subscription_intents_app_idx
    ON deployment_event_subscription_intents (app_id);
COMMENT ON TABLE deployment_event_subscription_intents IS 'Event subscriptions a production manifest deploy declares, applied when the deployment serves all production traffic; written only by apid (ADR-20261101030512).';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS deployment_event_subscription_intents;
-- +goose StatementEnd
