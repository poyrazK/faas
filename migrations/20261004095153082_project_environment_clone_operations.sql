-- +goose Up
-- A complete environment clone is a resumable operation. The source identity
-- and the target name are fixed before external data resources are prepared.
CREATE TABLE project_environment_clone_operations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_environment text NOT NULL,
    target_environment text NOT NULL,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    source_revision_hash text NOT NULL CHECK (source_revision_hash ~ '^[a-f0-9]{64}$'),
    source_release_set_id uuid,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN
        ('pending', 'capturing', 'copying', 'publishing', 'ready', 'failed', 'compensating', 'compensated')),
	    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    -- Only resource identities, capture points, and status belong here.
    -- Credentials and secret values must never enter this document.
    resources jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(resources) = 'array'),
    error_code text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT project_environment_clone_source_target_distinct
        CHECK (source_environment <> target_environment),
    CONSTRAINT project_environment_clone_idempotency_unique
        UNIQUE (account_id, project_id, idempotency_key)
);
-- A completed clone remains in history even after its environment is removed.
-- Only operations that can still create or publish a target reserve its name.
CREATE UNIQUE INDEX project_environment_clone_active_target_uniq
    ON project_environment_clone_operations (project_id, target_environment)
    WHERE status IN ('pending', 'capturing', 'copying', 'publishing', 'failed', 'compensating');
CREATE INDEX project_environment_clone_operations_status_idx
    ON project_environment_clone_operations (status, updated_at)
    WHERE status NOT IN ('ready', 'failed');

-- +goose Down
DROP TABLE project_environment_clone_operations;
