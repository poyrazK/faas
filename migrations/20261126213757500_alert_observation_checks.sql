-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS alert_observation_checks (
    rule_id uuid NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    evaluated_at timestamptz NOT NULL,
    skip_reason text NOT NULL,
    PRIMARY KEY (rule_id, evaluated_at)
);

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'faas_runtime') THEN
        GRANT SELECT, INSERT, DELETE ON alert_observation_checks TO faas_runtime;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE alert_observation_checks;
