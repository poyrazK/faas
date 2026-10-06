-- +goose Up
-- ADR-531: leases use the database clock. Claim and release advance the
-- operation revision, fencing all captured/prepared work from a previous owner.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid='project_environment_clone_operations'::regclass AND attname='lease_token' AND NOT attisdropped) THEN
        ALTER TABLE project_environment_clone_operations
            ADD COLUMN IF NOT EXISTS lease_token uuid,
            ADD COLUMN IF NOT EXISTS lease_until timestamptz,
            ADD COLUMN IF NOT EXISTS attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
            ADD COLUMN IF NOT EXISTS next_attempt_at timestamptz NOT NULL DEFAULT now(),
            ADD CONSTRAINT project_environment_clone_lease_pair
                CHECK ((lease_token IS NULL) = (lease_until IS NULL));
    END IF;
END;
$$;
-- +goose StatementEnd
CREATE INDEX IF NOT EXISTS project_environment_clone_worker_due_idx
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
