-- filename: 20261109121415204_gateway_edge_header_secrets.sql
-- migration-compat: expand
-- ADR-20261108110000 amendment 2026-11-09: a compute gateway reports that it
-- opens sealed edge-rule header values. The report holds the boot that made
-- it, so a gateway rolled back to a release that predates the column (whose
-- registration resets only the columns it knows) stops counting as able.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE gateway_traffic_runtime_observations
    ADD COLUMN IF NOT EXISTS edge_header_secrets_boot uuid;

-- +goose Down
SET LOCAL lock_timeout = '5s';
ALTER TABLE gateway_traffic_runtime_observations
    DROP COLUMN IF EXISTS edge_header_secrets_boot;
