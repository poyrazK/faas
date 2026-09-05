-- filename: 20260905120000000_daemon_deployments.sql
-- +goose Up
-- +goose StatementBegin

-- Operator-side platform release ledger. Unlike release_bundles (which
-- records an artifact), this table records what happened when an individual
-- daemon was installed. Rows are retained after the on-disk release tree is
-- pruned so incident response can answer who changed a daemon and whether the
-- change succeeded or was rolled back.
CREATE TABLE IF NOT EXISTS daemon_deployments (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    daemon          text NOT NULL CHECK (length(btrim(daemon)) > 0),
    version         text NOT NULL CHECK (length(btrim(version)) > 0),
    commit_sha      text NOT NULL CHECK (commit_sha ~ '^[a-f0-9]{40}$'),
    signed_by       text,
    sbom_sha256     text CHECK (sbom_sha256 IS NULL OR sbom_sha256 ~ '^sha256:[a-f0-9]{64}$'),
    deployed_by     text NOT NULL CHECK (length(btrim(deployed_by)) > 0),
    deployed_at     timestamptz NOT NULL DEFAULT now(),
    completed_at    timestamptz,
    deploy_kind     text NOT NULL CHECK (deploy_kind IN ('install', 'deploy', 'rollback', 'bootstrap', 'reconcile')),
    supersedes      uuid REFERENCES daemon_deployments(id),
    status          text NOT NULL DEFAULT 'in_progress'
                    CHECK (status IN ('in_progress', 'succeeded', 'rolled_back', 'failed')),
    notes           jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT daemon_deployments_completed_after_started
        CHECK (completed_at IS NULL OR completed_at >= deployed_at)
);

CREATE INDEX IF NOT EXISTS daemon_deployments_daemon_deployed_at_idx
    ON daemon_deployments (daemon, deployed_at DESC);

CREATE INDEX IF NOT EXISTS daemon_deployments_commit_deployed_at_idx
    ON daemon_deployments (commit_sha, deployed_at DESC);

-- A daemon can have only one live attempt. This protects the operator ledger
-- when a retry races with the original deploy while still allowing historical
-- failed and rolled-back rows to remain queryable.
CREATE UNIQUE INDEX IF NOT EXISTS daemon_deployments_daemon_in_progress_idx
    ON daemon_deployments (daemon)
    WHERE status = 'in_progress';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS daemon_deployments_daemon_in_progress_idx;
DROP INDEX IF EXISTS daemon_deployments_commit_deployed_at_idx;
DROP INDEX IF EXISTS daemon_deployments_daemon_deployed_at_idx;
DROP TABLE IF EXISTS daemon_deployments;

-- +goose StatementEnd
