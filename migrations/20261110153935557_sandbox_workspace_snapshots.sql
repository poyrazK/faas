-- filename: 20261110153935557_sandbox_workspace_snapshots.sql
-- migration-compat: expand

-- +goose Up
-- The foreign key locks accounts briefly, and the new column sandbox_sessions.
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
-- ADR-20261027090013 step 7: persisted sandbox workspaces. A persist call
-- reaches agentd on the session's stream; agentd reserves the archive's
-- bytes against SandboxSnapshotBytesPerAccount (a pending row), vmmd writes
-- the archive to the artifact store under storage_key, and agentd marks the
-- row ready with its size and digest. agentd's sweep deletes the archive and
-- the row past expires_at, a pending row it never completed, and a row apid
-- asked to delete (delete_requested_at). A new table and a nullable column,
-- so older binaries keep working while this lands.
CREATE TABLE IF NOT EXISTS sandbox_workspace_snapshots (
    id                  uuid PRIMARY KEY,
    account_id          uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    sandbox_id          uuid NOT NULL,
    state               text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'ready')),
    storage_key         text NOT NULL UNIQUE CHECK (char_length(storage_key) BETWEEN 1 AND 512),
    reserved_bytes      bigint NOT NULL CHECK (reserved_bytes > 0),
    bytes               bigint CHECK (bytes IS NULL OR bytes BETWEEN 0 AND reserved_bytes),
    sha256              text CHECK (sha256 IS NULL OR sha256 ~ '^[0-9a-f]{64}$'),
    expires_at          timestamptz NOT NULL,
    delete_requested_at timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    ready_at            timestamptz,
    CONSTRAINT sandbox_workspace_snapshots_ready_check CHECK (
        (state = 'ready') = (ready_at IS NOT NULL AND bytes IS NOT NULL AND sha256 IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS sandbox_workspace_snapshots_account_idx
    ON sandbox_workspace_snapshots (account_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS sandbox_workspace_snapshots_expiry_idx ON sandbox_workspace_snapshots (expires_at);
CREATE INDEX IF NOT EXISTS sandbox_workspace_snapshots_pending_idx ON sandbox_workspace_snapshots (created_at)
    WHERE state = 'pending';
CREATE INDEX IF NOT EXISTS sandbox_workspace_snapshots_delete_idx ON sandbox_workspace_snapshots (delete_requested_at)
    WHERE delete_requested_at IS NOT NULL;
COMMENT ON TABLE sandbox_workspace_snapshots IS
    'ADR-20261027090013 step 7 persisted sandbox workspaces; agentd inserts, completes and deletes rows; apid reads them and sets delete_requested_at.';
-- The workspace a session starts from (create's snapshot_id). No foreign
-- key: a snapshot deleted before the session boots fails the session with
-- workspace_unavailable instead of starting it empty.
ALTER TABLE sandbox_sessions ADD COLUMN IF NOT EXISTS workspace_snapshot_id uuid;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS workspace_snapshot_id;
DROP TABLE IF EXISTS sandbox_workspace_snapshots;
-- +goose StatementEnd
