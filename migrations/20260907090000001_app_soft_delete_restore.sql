-- +goose Up
-- +goose StatementBegin

-- H2 (issue #1397): customer app deletion is a restorable tombstone.
-- Keep the deadline on the app row so restore and the sweeper can make
-- their decisions atomically, without a separate queue table.
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
DROP INDEX IF EXISTS apps_delete_grace_idx;
ALTER TABLE apps
    DROP COLUMN IF EXISTS delete_grace_until,
    DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd
