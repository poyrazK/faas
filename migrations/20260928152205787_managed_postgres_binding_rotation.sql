-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_bindings
    ADD COLUMN IF NOT EXISTS rotation_previous_generation bigint,
    ADD COLUMN IF NOT EXISTS rotation_wake_id uuid,
    ADD COLUMN IF NOT EXISTS rotation_cleanup_ready boolean NOT NULL DEFAULT false;

ALTER TABLE managed_postgres_bindings
    DROP CONSTRAINT IF EXISTS managed_postgres_bindings_state_check;
ALTER TABLE managed_postgres_bindings
    ADD CONSTRAINT managed_postgres_bindings_state_check
    CHECK (state IN ('provisioning','ready','deleting','retiring','failed','deleted'));

ALTER TABLE managed_postgres_bindings
    DROP CONSTRAINT IF EXISTS managed_postgres_bindings_rotation_pair_check;
ALTER TABLE managed_postgres_bindings
    ADD CONSTRAINT managed_postgres_bindings_rotation_pair_check CHECK (
        (rotation_previous_generation IS NULL) = (rotation_wake_id IS NULL)
        AND (rotation_previous_generation IS NULL OR
            (rotation_previous_generation >= 1 AND rotation_previous_generation < credential_generation))
        AND (NOT rotation_cleanup_ready OR rotation_previous_generation IS NOT NULL)
    );

CREATE INDEX IF NOT EXISTS managed_postgres_bindings_rotation_reconcile_idx
    ON managed_postgres_bindings(retry_at, id)
    WHERE rotation_cleanup_ready AND state <> 'deleted';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS managed_postgres_bindings_rotation_reconcile_idx;
ALTER TABLE managed_postgres_bindings
    DROP CONSTRAINT IF EXISTS managed_postgres_bindings_rotation_pair_check;
ALTER TABLE managed_postgres_bindings
    DROP CONSTRAINT IF EXISTS managed_postgres_bindings_state_check;
ALTER TABLE managed_postgres_bindings
    ADD CONSTRAINT managed_postgres_bindings_state_check
    CHECK (state IN ('provisioning','ready','deleting','failed','deleted'));
ALTER TABLE managed_postgres_bindings
    DROP COLUMN IF EXISTS rotation_cleanup_ready,
    DROP COLUMN IF EXISTS rotation_wake_id,
    DROP COLUMN IF EXISTS rotation_previous_generation;
-- +goose StatementEnd
