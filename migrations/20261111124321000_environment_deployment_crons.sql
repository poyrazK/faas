-- filename: 20261111124321000_environment_deployment_crons.sql
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
-- ADR-20261104120000: `gregale deploy --environment X` stages the manifest's
-- HTTP crons into X. Production keeps its staging state on apps and its
-- activation in deployment_cron_activations (keyed by app, which N-1 writes
-- with ON CONFLICT(app_id)); every other environment has its own revision and
-- pending activation here, keyed by app and scope.
CREATE TABLE IF NOT EXISTS environment_deployment_cron_states (
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  deployment_scope text NOT NULL CHECK (deployment_scope ~ '^[a-z][a-z0-9-]{0,62}$'
    AND deployment_scope NOT IN ('default', 'production')),
  revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
  PRIMARY KEY (app_id, deployment_scope)
);
CREATE TABLE IF NOT EXISTS environment_deployment_cron_activations (
  app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  deployment_scope text NOT NULL CHECK (deployment_scope ~ '^[a-z][a-z0-9-]{0,62}$'
    AND deployment_scope NOT IN ('default', 'production')),
  upload_id text REFERENCES upload_sessions(id) ON DELETE SET NULL,
  session_id text NOT NULL CHECK (session_id ~ '^[0-9a-f]{32}$'),
  intent jsonb NOT NULL CHECK (jsonb_typeof(intent) = 'object'),
  fingerprint text NOT NULL CHECK (fingerprint ~ '^[0-9a-f]{64}$'),
  revision bigint NOT NULL CHECK (revision > 0),
  status text NOT NULL CHECK (status IN ('prepared', 'activated', 'conflict', 'discarded', 'canceled')),
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (app_id, deployment_scope)
);
CREATE INDEX IF NOT EXISTS environment_deployment_cron_activations_prepared_idx
  ON environment_deployment_cron_activations (updated_at, app_id) WHERE status = 'prepared';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS environment_deployment_cron_activations;
DROP TABLE IF EXISTS environment_deployment_cron_states;
-- +goose StatementEnd
