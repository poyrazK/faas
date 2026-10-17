-- filename: 20261017083124969_builds_queued_count_index.sql

-- +goose NO TRANSACTION

-- +goose Up
-- +goose StatementBegin
-- The apid queue observer counts queued rows every 30s. Keep historical
-- running/terminal builds out of that count's index; the existing build
-- indexes lead with deployment_id or cover only running builds.
CREATE INDEX CONCURRENTLY IF NOT EXISTS builds_queued_count_idx ON builds (id)
    WHERE status = 'queued';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS builds_queued_count_idx;
-- +goose StatementEnd
