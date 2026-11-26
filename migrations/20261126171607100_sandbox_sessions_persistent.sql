-- filename: 20261126171607100_sandbox_sessions_persistent.sql
-- migration-compat: expand
-- ADR-20261122132055 persistent sandbox sessions. A session of kind
-- persistent can be paused with its memory kept and resumed, forked, given a
-- workspace volume and a larger shape. This widens the state and RAM checks
-- (the previous release never writes the new states or sizes) and adds the
-- columns pause, resume and fork need. agentd owns the new transitions; apid
-- writes only pause_requested_at and resume_requested_at and inserts forks.
-- Each wider CHECK is added under a new name NOT VALID, validated, and only
-- then is the old one dropped; indexes build concurrently. Every step is
-- re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';
-- sandbox_sessions holds at most a few thousand rows; the index builds take
-- well under the bound.
SET statement_timeout = '10min';
ALTER TABLE sandbox_sessions
    ADD COLUMN IF NOT EXISTS vcpu smallint,
    ADD COLUMN IF NOT EXISTS volume_gb integer,
    ADD COLUMN IF NOT EXISTS pause_requested_at timestamptz,
    ADD COLUMN IF NOT EXISTS resume_requested_at timestamptz,
    ADD COLUMN IF NOT EXISTS paused_at timestamptz,
    ADD COLUMN IF NOT EXISTS durability text,
    ADD COLUMN IF NOT EXISTS resumed_cold boolean NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS forked_from uuid,
    -- resume_not_before: a resume refused for want of its capture's node
    -- is claimed again only after it.
    ADD COLUMN IF NOT EXISTS resume_not_before timestamptz;
-- A resumed session runs a new VM whose usage counters start at zero. Each
-- usage row remembers the lease (one per VM run) it was written under and
-- the totals of the runs before it, so the session's usage adds up across
-- pauses instead of stopping at the first one.
ALTER TABLE sandbox_session_usage
    ADD COLUMN IF NOT EXISTS lease_token text,
    ADD COLUMN IF NOT EXISTS base_samples integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_expected_samples integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_measured_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_paused_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_frozen_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_private_mb_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_unpaused_private_mb_ms bigint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS base_billable_mb_ms bigint NOT NULL DEFAULT 0;
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_state_v2_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_state_v2_check CHECK (state IN (
            'queued', 'starting', 'ready', 'frozen', 'pausing', 'paused', 'resuming', 'closed', 'expired', 'failed')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_ram_mb_v2_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_ram_mb_v2_check
            CHECK (ram_mb BETWEEN 512 AND 32768 AND ram_mb % 512 = 0) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_vcpu_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_vcpu_check
            CHECK (vcpu IS NULL OR vcpu BETWEEN 1 AND 16) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_volume_gb_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_volume_gb_check
            CHECK (volume_gb IS NULL OR (kind = 'persistent' AND volume_gb BETWEEN 1 AND 1024)) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_durability_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_durability_check
            CHECK (durability IS NULL OR durability IN ('node', 'remote')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_paused_check' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_paused_check
            CHECK ((state = 'paused') <= (paused_at IS NOT NULL AND kind = 'persistent')) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_session_usage_base_check' AND conrelid = 'sandbox_session_usage'::regclass) THEN
        ALTER TABLE sandbox_session_usage ADD CONSTRAINT sandbox_session_usage_base_check
            CHECK (base_measured_ms BETWEEN 0 AND measured_ms) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'sandbox_sessions_forked_from_fkey' AND conrelid = 'sandbox_sessions'::regclass) THEN
        ALTER TABLE sandbox_sessions ADD CONSTRAINT sandbox_sessions_forked_from_fkey
            FOREIGN KEY (forked_from) REFERENCES sandbox_sessions (id) ON DELETE SET NULL NOT VALID;
    END IF;
END
$migration$;
-- +goose StatementEnd
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_state_v2_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_ram_mb_v2_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_vcpu_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_volume_gb_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_durability_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_paused_check;
ALTER TABLE sandbox_sessions VALIDATE CONSTRAINT sandbox_sessions_forked_from_fkey;
ALTER TABLE sandbox_session_usage VALIDATE CONSTRAINT sandbox_session_usage_base_check;
-- The wider checks replace the narrow ones; the previous release writes
-- only states and sizes both accept.
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_state_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_ram_mb_check;
-- Paused sessions per account (SandboxPersistentMax) and agentd's retention
-- sweep read paused sessions by account and by age.
-- +goose StatementBegin
DO $$ BEGIN
 -- A failed concurrent build leaves an invalid index that IF NOT EXISTS
 -- would keep; drop it so a rerun builds it again.
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'sandbox_sessions_paused_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX sandbox_sessions_paused_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'sandbox_sessions_persistent_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX sandbox_sessions_persistent_idx';
 END IF;
 IF EXISTS (SELECT 1 FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
            WHERE c.relname = 'sandbox_sessions_forked_from_idx' AND NOT i.indisvalid) THEN
  EXECUTE 'DROP INDEX sandbox_sessions_forked_from_idx';
 END IF;
END $$;
-- +goose StatementEnd
CREATE INDEX CONCURRENTLY IF NOT EXISTS sandbox_sessions_paused_idx ON sandbox_sessions (paused_at, id) WHERE state = 'paused';
CREATE INDEX CONCURRENTLY IF NOT EXISTS sandbox_sessions_persistent_idx ON sandbox_sessions (account_id)
    WHERE kind = 'persistent' AND state IN ('queued', 'starting', 'ready', 'frozen', 'pausing', 'paused', 'resuming');
CREATE INDEX CONCURRENTLY IF NOT EXISTS sandbox_sessions_forked_from_idx ON sandbox_sessions (forked_from) WHERE forked_from IS NOT NULL;

-- +goose Down
SET lock_timeout = '5s';
DROP INDEX CONCURRENTLY IF EXISTS sandbox_sessions_forked_from_idx;
DROP INDEX CONCURRENTLY IF EXISTS sandbox_sessions_persistent_idx;
DROP INDEX CONCURRENTLY IF EXISTS sandbox_sessions_paused_idx;
ALTER TABLE sandbox_session_usage DROP CONSTRAINT IF EXISTS sandbox_session_usage_base_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_forked_from_fkey;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_paused_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_durability_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_volume_gb_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_vcpu_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_ram_mb_v2_check;
ALTER TABLE sandbox_sessions DROP CONSTRAINT IF EXISTS sandbox_sessions_state_v2_check;
ALTER TABLE sandbox_sessions
    DROP COLUMN IF EXISTS resume_not_before,
    DROP COLUMN IF EXISTS forked_from,
    DROP COLUMN IF EXISTS resumed_cold,
    DROP COLUMN IF EXISTS durability,
    DROP COLUMN IF EXISTS paused_at,
    DROP COLUMN IF EXISTS resume_requested_at,
    DROP COLUMN IF EXISTS pause_requested_at,
    DROP COLUMN IF EXISTS volume_gb,
    DROP COLUMN IF EXISTS vcpu;
ALTER TABLE sandbox_session_usage
    DROP COLUMN IF EXISTS base_billable_mb_ms,
    DROP COLUMN IF EXISTS base_unpaused_private_mb_ms,
    DROP COLUMN IF EXISTS base_private_mb_ms,
    DROP COLUMN IF EXISTS base_frozen_ms,
    DROP COLUMN IF EXISTS base_paused_ms,
    DROP COLUMN IF EXISTS base_measured_ms,
    DROP COLUMN IF EXISTS base_expected_samples,
    DROP COLUMN IF EXISTS base_samples,
    DROP COLUMN IF EXISTS lease_token;
