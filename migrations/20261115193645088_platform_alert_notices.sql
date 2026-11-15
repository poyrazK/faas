-- filename: 20261115193645088_platform_alert_notices.sql
-- migration-compat: expand
-- Platform alerts (ADR-20261115194000), from the 2026-11-15 prod findings:
-- an app answering only 5xx for hours raised no alert unless its owner had
-- written an alert rule, and a cron failing every run never stopped.
--
-- crons.suspended_reason gains 'failing': schedd pauses an HTTP cron whose
-- recent runs all failed; editing the cron or promoting a deployment resumes
-- it. The wider CHECK is added NOT VALID under a new name, validated, and
-- only then is the old one dropped, each in its own statement, so crons
-- keep being written. Older schedd and apid never write 'failing'.
--
-- platform_alert_notices holds the emails the platform owes an account for
-- such a condition. schedd and meterd queue them, meterd sends them. One
-- pending row per (kind, subject); finished rows stay for the cooldown
-- check and meterd trims them. Every step is re-runnable.
-- +goose NO TRANSACTION

-- +goose Up
SET lock_timeout = '5s';

-- +goose StatementBegin
DO $failing$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'crons'::regclass AND conname = 'crons_suspended_reason_v2_chk') THEN
    ALTER TABLE crons ADD CONSTRAINT crons_suspended_reason_v2_chk
      CHECK (suspended_reason IN ('', 'no_live_deployment', 'app_deleted', 'failing')) NOT VALID;
  END IF;
END
$failing$;
-- +goose StatementEnd

ALTER TABLE crons VALIDATE CONSTRAINT crons_suspended_reason_v2_chk;

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_suspended_reason_chk;

CREATE TABLE IF NOT EXISTS platform_alert_notices (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('app_outage', 'cron_failing_paused')),
    subject_id text NOT NULL CHECK (subject_id <> '' AND length(subject_id) <= 128),
    detail jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(detail) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'skipped', 'failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_expires_at timestamptz,
    finished_at timestamptz,
    last_error text NOT NULL DEFAULT '' CHECK (length(last_error) <= 512)
);

CREATE UNIQUE INDEX IF NOT EXISTS platform_alert_notices_pending_uniq
    ON platform_alert_notices (kind, subject_id) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS platform_alert_notices_due_idx
    ON platform_alert_notices (next_attempt_at) WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS platform_alert_notices_subject_idx
    ON platform_alert_notices (kind, subject_id, created_at DESC);

-- +goose StatementBegin
DO $grants$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'faas_runtime') THEN
    GRANT SELECT, INSERT, UPDATE, DELETE ON platform_alert_notices TO faas_runtime;
  END IF;
END
$grants$;
-- +goose StatementEnd

RESET lock_timeout;

-- +goose Down
SET lock_timeout = '5s';

DROP TABLE IF EXISTS platform_alert_notices;

UPDATE crons SET suspended_reason = '' WHERE suspended_reason = 'failing';

-- +goose StatementBegin
DO $restore$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'crons'::regclass AND conname = 'crons_suspended_reason_chk') THEN
    ALTER TABLE crons ADD CONSTRAINT crons_suspended_reason_chk
      CHECK (suspended_reason IN ('', 'no_live_deployment', 'app_deleted'));
  END IF;
END
$restore$;
-- +goose StatementEnd

ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_suspended_reason_v2_chk;

RESET lock_timeout;
