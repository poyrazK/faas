-- filename: 20261126170306741_api_sandbox_webhook_partners.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';

ALTER TABLE api_sandbox_runs
    ADD COLUMN partner_webhook boolean NOT NULL DEFAULT false,
    ADD COLUMN partner_outcome_until timestamptz;

CREATE TABLE api_sandbox_webhook_partners (
    run_id uuid PRIMARY KEY,
    source_deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    scenario_revision uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    invite_hash bytea,
    target_url text NOT NULL DEFAULT '',
    signing_secret_sealed bytea,
    outcome_token_hash bytea,
    ready_at timestamptz,
    outcome_count integer CHECK (outcome_count BETWEEN 0 AND 1000),
    outcome_at timestamptz,
    CHECK ((outcome_at IS NULL) = (outcome_count IS NULL))
);

CREATE INDEX api_sandbox_webhook_partners_source ON api_sandbox_webhook_partners (source_deployment_id);
CREATE INDEX api_sandbox_webhook_partners_expiry ON api_sandbox_webhook_partners (expires_at);

-- +goose Down
DROP TABLE api_sandbox_webhook_partners;
ALTER TABLE api_sandbox_runs DROP COLUMN partner_outcome_until, DROP COLUMN partner_webhook;
