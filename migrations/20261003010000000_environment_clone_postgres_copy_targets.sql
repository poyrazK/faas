-- +goose Up
-- ADR-375: an independent provider project is owned before dispatch. Native
-- captures and prepared projects supply no complete data-copy/readiness proof.
CREATE TABLE project_environment_clone_postgres_copy_targets (
    operation_id uuid NOT NULL,
    source_database_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES accounts(id),
    capture_database_id uuid NOT NULL REFERENCES managed_postgres_databases(id),
    target_database_id uuid NOT NULL UNIQUE REFERENCES managed_postgres_databases(id),
    state text NOT NULL DEFAULT 'reserved' CHECK (state IN ('reserved','requested','preparing','prepared','retired')),
    request_started_at timestamptz,
    provider_resource_id text CHECK (provider_resource_id IS NULL OR length(provider_resource_id) BETWEEN 1 AND 255),
    provider_created_at timestamptz,
    observed_at timestamptz,
    prepared_at timestamptz,
    retired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (operation_id,source_database_id),
    FOREIGN KEY (operation_id,source_database_id) REFERENCES project_environment_clone_postgres_snapshot_restores(operation_id,source_database_id),
    CHECK (target_database_id<>capture_database_id AND target_database_id<>source_database_id),
    CHECK ((state='reserved' AND request_started_at IS NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NULL)
        OR (state='requested' AND request_started_at IS NOT NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NULL)
        OR (state='preparing' AND request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND prepared_at IS NULL AND retired_at IS NULL)
        OR (state='prepared' AND request_started_at IS NOT NULL AND provider_resource_id IS NOT NULL AND provider_created_at IS NOT NULL AND observed_at IS NOT NULL AND prepared_at IS NOT NULL AND retired_at IS NULL)
        OR (state='retired' AND request_started_at IS NULL AND provider_resource_id IS NULL AND provider_created_at IS NULL AND observed_at IS NULL AND prepared_at IS NULL AND retired_at IS NOT NULL)),
    CHECK (prepared_at IS NULL OR prepared_at>=provider_created_at),
    CHECK (observed_at IS NULL OR observed_at>=provider_created_at)
);
CREATE INDEX project_environment_clone_postgres_copy_capture_holds
    ON project_environment_clone_postgres_copy_targets(capture_database_id) WHERE state<>'retired';

-- +goose Down
-- Even retired ownership must survive until the owning operation is removed.
ALTER TABLE project_environment_clone_postgres_copy_targets
    ADD CONSTRAINT copy_targets_down_no_ownership CHECK (false);
DROP TABLE project_environment_clone_postgres_copy_targets;
