-- filename: 20260926081111526_deployment_readiness_probe.sql
-- A deployment's recurring primary-app readiness policy is immutable and
-- independent from its one-shot startup healthcheck and liveness policy.
--
-- +goose Up
ALTER TABLE deployments
  ADD COLUMN IF NOT EXISTS override_readiness_probe jsonb;

-- +goose Down
ALTER TABLE deployments
  DROP COLUMN IF EXISTS override_readiness_probe;
