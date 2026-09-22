-- +goose Up
-- +goose StatementBegin
-- ADR-201 §3: per-upstream egress circuit-breaker opt-in.
--
-- The default is FALSE and that is load-bearing, not conservatism. An open
-- circuit REJECTS a tenant's connections to their own database. Enabling that
-- implicitly for every captured upstream would mean an ADR-098 inference
-- (which reads a DATABASE_URL-shaped env var) silently gains the power to cut
-- an app off from its data store. The operator flips the node flag; the
-- customer opts in per upstream.
--
-- Thresholds are nullable: NULL means "use the platform default"
-- (circuit.EgressConfig), so a row that only sets enabled=true tracks the
-- defaults as they evolve rather than freezing today's values.
ALTER TABLE data_upstreams
  ADD COLUMN IF NOT EXISTS circuit_breaker_enabled boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS circuit_breaker_failure_threshold double precision,
  ADD COLUMN IF NOT EXISTS circuit_breaker_min_samples integer,
  ADD COLUMN IF NOT EXISTS circuit_breaker_open_seconds integer;

ALTER TABLE data_upstreams
  DROP CONSTRAINT IF EXISTS data_upstreams_circuit_threshold_check;
ALTER TABLE data_upstreams
  ADD CONSTRAINT data_upstreams_circuit_threshold_check
  CHECK (circuit_breaker_failure_threshold IS NULL
         OR (circuit_breaker_failure_threshold > 0
             AND circuit_breaker_failure_threshold <= 1));

ALTER TABLE data_upstreams
  DROP CONSTRAINT IF EXISTS data_upstreams_circuit_min_samples_check;
ALTER TABLE data_upstreams
  ADD CONSTRAINT data_upstreams_circuit_min_samples_check
  CHECK (circuit_breaker_min_samples IS NULL
         OR (circuit_breaker_min_samples >= 1
             AND circuit_breaker_min_samples <= 1000));

ALTER TABLE data_upstreams
  DROP CONSTRAINT IF EXISTS data_upstreams_circuit_open_seconds_check;
ALTER TABLE data_upstreams
  ADD CONSTRAINT data_upstreams_circuit_open_seconds_check
  CHECK (circuit_breaker_open_seconds IS NULL
         OR (circuit_breaker_open_seconds >= 1
             AND circuit_breaker_open_seconds <= 3600));

-- schedd's reconcile loop sweeps only the opted-in rows. A partial index keeps
-- that sweep proportional to the opt-in count rather than to the whole
-- data_upstreams table, which grows with every captured env var on every app.
CREATE INDEX IF NOT EXISTS data_upstreams_circuit_enabled_idx
  ON data_upstreams (app_id, host_redacted_hash)
  WHERE circuit_breaker_enabled;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS data_upstreams_circuit_enabled_idx;
ALTER TABLE data_upstreams
  DROP CONSTRAINT IF EXISTS data_upstreams_circuit_open_seconds_check,
  DROP CONSTRAINT IF EXISTS data_upstreams_circuit_min_samples_check,
  DROP CONSTRAINT IF EXISTS data_upstreams_circuit_threshold_check;
ALTER TABLE data_upstreams
  DROP COLUMN IF EXISTS circuit_breaker_open_seconds,
  DROP COLUMN IF EXISTS circuit_breaker_min_samples,
  DROP COLUMN IF EXISTS circuit_breaker_failure_threshold,
  DROP COLUMN IF EXISTS circuit_breaker_enabled;
-- +goose StatementEnd
