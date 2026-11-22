-- migration-compat: expand
-- ADR-20261122132055 §4, §11: persistent sessions' workspace volumes and
-- their backups. agentd records where a session's newest volume copy is
-- (sandbox_volumes.node_id) and its newest backup, one row per backup vmmd
-- reported, and deletes superseded backups' manifests and an ended session's
-- volume and backups. The previous release never reads these tables.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS sandbox_volumes (
 session_id uuid PRIMARY KEY REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 -- node_id holds the newest copy; another node's copy is stale.
 node_id text NOT NULL CHECK (node_id <> ''),
 size_gb integer NOT NULL CHECK (size_gb > 0),
 latest_backup_id uuid,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sandbox_volume_backups (
 id uuid PRIMARY KEY,
 session_id uuid NOT NULL REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 node_id text NOT NULL CHECK (node_id <> ''),
 -- periodic backups are stored when reported; a pause's uploads after the
 -- pause, and agentd marks it stored once the shared store holds it.
 reason text NOT NULL CHECK (reason IN ('periodic','pause')),
 state text NOT NULL CHECK (state IN ('uploading','stored','deleted')),
 size_bytes bigint NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
 data_chunks integer NOT NULL DEFAULT 0 CHECK (data_chunks >= 0),
 uploaded_bytes bigint NOT NULL DEFAULT 0 CHECK (uploaded_bytes >= 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 deleted_at timestamptz,
 CHECK ((state = 'deleted') = (deleted_at IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS sandbox_volume_backups_session ON sandbox_volume_backups(session_id, created_at) WHERE state <> 'deleted';

-- +goose Down
DROP TABLE IF EXISTS sandbox_volume_backups;
DROP TABLE IF EXISTS sandbox_volumes;
