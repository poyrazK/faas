-- +goose Up
-- ADR-531: leases use the database clock. Claim and release advance the
-- operation revision, fencing all captured/prepared work from a previous owner.
ALTER TABLE project_environment_clone_operations
    ADD COLUMN lease_token uuid,
    ADD COLUMN lease_until timestamptz,
    ADD COLUMN attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(),
    ADD CONSTRAINT project_environment_clone_lease_pair
        CHECK ((lease_token IS NULL) = (lease_until IS NULL));
CREATE INDEX project_environment_clone_worker_due_idx
    ON project_environment_clone_operations (next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'capturing', 'copying', 'publishing', 'compensating');

-- +goose Down
DROP INDEX project_environment_clone_worker_due_idx;
ALTER TABLE project_environment_clone_operations
    DROP CONSTRAINT project_environment_clone_lease_pair,
    DROP COLUMN lease_token,
    DROP COLUMN lease_until,
    DROP COLUMN attempt_count,
    DROP COLUMN next_attempt_at;
