-- migration-compat: expand
-- filename: 20261019103207293_app_release_gates.sql
--
-- ADR-20261019071734: release gates on customer signals. An app declares
-- gates on its SLOs, its captured errors and its custom metrics; the canary
-- circuit breaker and the post-release watch abort or roll back a release
-- that fails one. Deleting an SLO deletes the gates that use it.

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS app_release_gates (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 name text NOT NULL CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
 kind text NOT NULL CHECK (kind IN ('slo','new_errors','custom_metric')),
 slo_id uuid REFERENCES app_slos(id) ON DELETE CASCADE,
 max_burn_rate integer CHECK (max_burn_rate IS NULL OR max_burn_rate BETWEEN 1 AND 1000),
 max_new_groups integer CHECK (max_new_groups IS NULL OR max_new_groups BETWEEN 0 AND 100),
 min_events integer CHECK (min_events IS NULL OR min_events BETWEEN 1 AND 100000),
 metric_name text CHECK (metric_name IS NULL OR metric_name ~ '^[a-z][a-z0-9_]{0,62}$'),
 comparison text CHECK (comparison IS NULL OR comparison IN ('below','above')),
 threshold double precision CHECK (threshold IS NULL OR (threshold = threshold AND threshold > '-Infinity'::double precision AND threshold < 'Infinity'::double precision)),
 min_samples integer NOT NULL CHECK (min_samples BETWEEN 1 AND 1000000),
 created_by text NOT NULL CHECK (length(created_by) BETWEEN 1 AND 320),
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE (app_id, name),
 CHECK (kind <> 'slo' OR (slo_id IS NOT NULL AND max_burn_rate IS NOT NULL
   AND max_new_groups IS NULL AND min_events IS NULL AND metric_name IS NULL AND comparison IS NULL AND threshold IS NULL)),
 CHECK (kind <> 'new_errors' OR (max_new_groups IS NOT NULL AND min_events IS NOT NULL
   AND slo_id IS NULL AND max_burn_rate IS NULL AND metric_name IS NULL AND comparison IS NULL AND threshold IS NULL)),
 CHECK (kind <> 'custom_metric' OR (metric_name IS NOT NULL AND comparison IS NOT NULL AND threshold IS NOT NULL
   AND slo_id IS NULL AND max_burn_rate IS NULL AND max_new_groups IS NULL AND min_events IS NULL))
);
CREATE INDEX IF NOT EXISTS app_release_gates_slo ON app_release_gates(slo_id) WHERE slo_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DROP TABLE IF EXISTS app_release_gates;
-- +goose StatementEnd
