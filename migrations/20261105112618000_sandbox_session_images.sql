-- filename: 20261105112618000_sandbox_session_images.sql
-- migration-compat: expand

-- +goose Up
-- The columns and their foreign key lock sandbox_sessions and jobs briefly.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 seller-image sessions: a session started from an OCI
-- image keeps the normalized reference it asked for and the image-run
-- runner job (ADR-1161) that caches it per account. apid sets both at
-- create; agentd claims the session only once the job's image is ready,
-- and fails it as image_unavailable when the image cannot be had. Nullable
-- additions, so older binaries keep working while this lands.
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS image_ref text CHECK (image_ref IS NULL OR char_length(image_ref) BETWEEN 1 AND 512),
    ADD COLUMN IF NOT EXISTS image_job_id uuid REFERENCES jobs(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS sandbox_sessions_image_job_idx ON sandbox_sessions (image_job_id)
    WHERE image_job_id IS NOT NULL AND state IN ('queued', 'starting', 'ready', 'frozen');
COMMENT ON COLUMN sandbox_sessions.image_job_id IS
    'ADR-20261027090013: the image-run runner job (ADR-1161) caching the session image; a live session keeps it from eviction.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS sandbox_sessions_image_job_idx;
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS image_job_id, DROP COLUMN IF EXISTS image_ref;
-- +goose StatementEnd
