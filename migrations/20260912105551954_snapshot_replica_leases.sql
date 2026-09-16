-- +goose Up
-- +goose StatementBegin

-- A syncing replica row can be reclaimed after a worker crash. The original
-- worker may still finish its storage read after that timeout, so completion
-- must be fenced to the specific claim that started it.
ALTER TABLE snapshot_replicas
    ADD COLUMN IF NOT EXISTS lease_token uuid;

CREATE INDEX IF NOT EXISTS snapshot_replicas_lease_token_idx
    ON snapshot_replicas (lease_token)
    WHERE lease_token IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS snapshot_replicas_lease_token_idx;
ALTER TABLE snapshot_replicas
    DROP COLUMN IF EXISTS lease_token;
-- +goose StatementEnd
