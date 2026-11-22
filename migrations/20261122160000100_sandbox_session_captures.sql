-- migration-compat: expand
-- ADR-20261122132055 §2, §11: a paused persistent session's pause captures.
-- agentd writes a row from vmmd's paused report, marks it consumed once a
-- resume restored it (a capture is used once), and deletes the objects of a
-- consumed capture, or of any capture of a session that ended, before it
-- removes the row. At most one capture of a session is live. The previous
-- release never reads the table. A capture is written to its node first and
-- uploaded after; agentd marks the session's durability remote once the
-- shared store holds all of it.
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS sandbox_session_captures (
 id uuid PRIMARY KEY,
 session_id uuid NOT NULL REFERENCES sandbox_sessions(id) ON DELETE CASCADE,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 node_id text NOT NULL CHECK (node_id <> ''),
 storage_key text NOT NULL CHECK (storage_key LIKE 'snap/sandbox-session-%/captures/%/v2/mem'),
 vmstate_storage_key text NOT NULL CHECK (vmstate_storage_key LIKE 'snap/sandbox-session-%/captures/%/v2/vmstate'),
 fc_version text NOT NULL CHECK (fc_version <> ''),
 mem_bytes bigint NOT NULL CHECK (mem_bytes > 0),
 vmstate_bytes bigint NOT NULL DEFAULT 0 CHECK (vmstate_bytes >= 0),
 -- has_backing: vmmd wrote the capture's ADR-510 backing identity, so the
 -- shared store's copy is complete (durability remote) only with it.
 has_backing boolean NOT NULL DEFAULT false,
 state text NOT NULL DEFAULT 'live' CHECK (state IN ('live','consumed')),
 created_at timestamptz NOT NULL DEFAULT now(),
 consumed_at timestamptz,
 CHECK ((state = 'consumed') = (consumed_at IS NOT NULL))
);
CREATE UNIQUE INDEX IF NOT EXISTS sandbox_session_captures_live ON sandbox_session_captures(session_id) WHERE state = 'live';
CREATE INDEX IF NOT EXISTS sandbox_session_captures_consumed ON sandbox_session_captures(consumed_at) WHERE state = 'consumed';

-- +goose Down
DROP TABLE IF EXISTS sandbox_session_captures;
