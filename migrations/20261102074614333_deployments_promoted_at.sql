-- filename: 20261102074614333_deployments_promoted_at.sql

-- ADR-20261028110044 amendment 4: when a traffic change last took this
-- deployment to 100% of its scope, for example promoting a release staged with
-- --no-traffic. The production route monitor anchors its windows and its
-- early-incident rollback window on it, as it does on canary stages and
-- rollout completion. Nullable without a default, so the change is
-- metadata-only.
-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS promoted_at timestamptz;

-- +goose Down
ALTER TABLE deployments DROP COLUMN IF EXISTS promoted_at;
