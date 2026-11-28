-- filename: 20261127074004247_sandbox_session_forks.sql
-- migration-compat: contract
-- +goose NO TRANSACTION

-- +goose Up
-- ADR-20261122132055 §3: forks. A fork is a queued session forked_from its
-- source that restores fork_capture_id: the source's live fork capture, or
-- its pause capture when it was paused. apid sets fork_requested_at on a
-- running source; agentd takes the capture, hands it to the waiting forks
-- and clears it. A fork starts on the capture's node before the upload;
-- fork_local_refused_at records that the node refused it, and the fork then
-- waits for the upload and starts on any node.
--
-- A capture now has a purpose: a pause's is restored once by its session's
-- resume, a fork's by each of the source's forks, so only pause captures
-- stay one live per session. uploaded_at records that the shared store
-- holds the whole capture. A fork carries the source's volume, so a forked
-- session may have one, and a fork's capture takes a volume backup of reason
-- fork.
--
-- The previous release writes only pause captures and periodic/pause
-- backups, never reads the new columns, and never forks, so every wider
-- check and the narrower unique index accept what it writes. Each wider
-- CHECK is added under a new name NOT VALID, validated, and only then is the
-- old one dropped; the index builds concurrently. Every step is re-runnable.
-- Contract only because it drops a unique index (the old live-capture one,
-- no ON CONFLICT target): a full release applies it.
SET lock_timeout = '5s';
-- Both tables hold at most a few thousand rows.
SET statement_timeout = '10min';
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS fork_capture_id uuid,
    ADD COLUMN IF NOT EXISTS fork_requested_at timestamptz,
    ADD COLUMN IF NOT EXISTS fork_local_refused_at timestamptz;
ALTER TABLE sandbox_session_captures
    ADD COLUMN IF NOT EXISTS purpose text NOT NULL DEFAULT 'pause',
    ADD COLUMN IF NOT EXISTS uploaded_at timestamptz;
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_session_captures_purpose_check' AND conrelid = 'sandbox_session_captures'::regclass) THEN
        ALTER TABLE sandbox_session_captures ADD CONSTRAINT sandbox_session_captures_purpose_check
            CHECK (purpose IN ('pause', 'fork')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_volume_gb_v2_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_volume_gb_v2_check
            CHECK (volume_gb IS NULL OR ((kind = 'persistent' OR forked_from IS NOT NULL) AND volume_gb BETWEEN 1 AND 1024)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_volume_backups_reason_v2_check' AND conrelid = 'sandbox_volume_backups'::regclass) THEN
        ALTER TABLE sandbox_volume_backups ADD CONSTRAINT sandbox_volume_backups_reason_v2_check
            CHECK (reason IN ('periodic', 'pause', 'fork')) NOT VALID;
    END IF;
END
$migration$;
-- +goose StatementEnd
ALTER TABLE sandbox_session_captures VALIDATE CONSTRAINT sandbox_session_captures_purpose_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_volume_gb_v2_check;
ALTER TABLE sandbox_volume_backups VALIDATE CONSTRAINT sandbox_volume_backups_reason_v2_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_volume_gb_check;
ALTER TABLE sandbox_volume_backups DROP CONSTRAINT IF EXISTS sandbox_volume_backups_reason_check;
-- One live pause capture per session; fork captures sit beside it.
-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'sandbox_session_captures_live_pause' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX sandbox_session_captures_live_pause';
 END IF;
END $$;
-- +goose StatementEnd
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS sandbox_session_captures_live_pause
    ON sandbox_session_captures (session_id) WHERE state = 'live' AND purpose = 'pause';
DROP INDEX CONCURRENTLY IF EXISTS sandbox_session_captures_live;

-- +goose Down
SET lock_timeout = '5s';
-- Fork rows break the narrower checks: they go first.
DELETE FROM sandbox_session_captures WHERE purpose = 'fork';
DELETE FROM sandbox_volume_backups WHERE reason = 'fork';
UPDATE sandbox_sessions SET volume_gb = NULL WHERE kind <> 'persistent' AND volume_gb IS NOT NULL;
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS sandbox_session_captures_live
    ON sandbox_session_captures (session_id) WHERE state = 'live';
DROP INDEX CONCURRENTLY IF EXISTS sandbox_session_captures_live_pause;
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_volume_gb_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_volume_gb_check
            CHECK (volume_gb IS NULL OR (kind = 'persistent' AND volume_gb BETWEEN 1 AND 1024));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_volume_backups_reason_check' AND conrelid = 'sandbox_volume_backups'::regclass) THEN
        ALTER TABLE sandbox_volume_backups ADD CONSTRAINT sandbox_volume_backups_reason_check
            CHECK (reason IN ('periodic', 'pause'));
    END IF;
END
$migration$;
-- +goose StatementEnd
ALTER TABLE sandbox_volume_backups DROP CONSTRAINT IF EXISTS sandbox_volume_backups_reason_v2_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_volume_gb_v2_check;
ALTER TABLE sandbox_session_captures DROP CONSTRAINT IF EXISTS sandbox_session_captures_purpose_check;
ALTER TABLE sandbox_session_captures DROP COLUMN IF EXISTS uploaded_at, DROP COLUMN IF EXISTS purpose;
ALTER TABLE sandbox_sessions DROP COLUMN IF EXISTS fork_local_refused_at, DROP COLUMN IF EXISTS fork_requested_at,
    DROP COLUMN IF EXISTS fork_capture_id;
