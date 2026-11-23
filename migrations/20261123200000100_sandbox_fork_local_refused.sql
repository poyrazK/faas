-- migration-compat: expand
-- ADR-20261122132055 §3 amendment (same-node forks): a fork starts on its
-- source's node from that node's copy of the capture before the shared
-- store holds it. fork_local_refused_at records that the node refused it
-- (no room, or gone); the fork then waits for the upload and starts on any
-- node. The previous release never reads it.
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE sandbox_sessions ADD COLUMN IF NOT EXISTS fork_local_refused_at timestamptz;

-- +goose Down
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS fork_local_refused_at;
