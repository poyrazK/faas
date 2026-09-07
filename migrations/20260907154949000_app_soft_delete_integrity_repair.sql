-- A shared timestamp was briefly merged for three independent migrations.
-- An environment that applied one branch before the collision was resolved may
-- have version 20260907090000000 in its goose ledger without the app tombstone
-- columns. Reassert the soft-delete shape at a unique version so every ledger
-- history converges before binaries query apps.deleted_at.
-- +goose Up
-- +goose StatementBegin

ALTER TABLE apps
    ADD COLUMN IF NOT EXISTS deleted_at timestamptz,
    ADD COLUMN IF NOT EXISTS delete_grace_until timestamptz;

CREATE INDEX IF NOT EXISTS apps_delete_grace_idx
    ON apps (delete_grace_until)
    WHERE status = 'deleted' AND delete_grace_until IS NOT NULL;

COMMENT ON COLUMN apps.deleted_at IS
    'Customer-requested app soft-delete timestamp; NULL for live and legacy tombstones.';
COMMENT ON COLUMN apps.delete_grace_until IS
    'Deadline through which a deleted app may be restored; hard-delete sweeper runs after this instant.';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Integrity repairs are forward-only: an earlier migration may own these
-- columns, so rolling this version back must not remove shared schema.
SELECT 1;
-- +goose StatementEnd
