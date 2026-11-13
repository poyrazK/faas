-- filename: 20261112120000514_secret_open_audit_jobs.sql
-- migration-compat: expand
-- ADR-20261110210000 slice 3j-2b: secretd pulls a job's private image for the
-- node that holds the job's materialization lease. Jobs belong to an account,
-- not an app, so an open of a job's registry credential is recorded under
-- job_id; every row names exactly one of app_id and job_id.

-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE secret_open_audit ADD COLUMN IF NOT EXISTS job_id uuid;
ALTER TABLE secret_open_audit ALTER COLUMN app_id DROP NOT NULL;
-- +goose StatementBegin
DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'secret_open_audit_subject_check') THEN
        ALTER TABLE secret_open_audit ADD CONSTRAINT secret_open_audit_subject_check
            CHECK ((app_id IS NULL) <> (job_id IS NULL)) NOT VALID;
    END IF;
END
$migration$;
-- +goose StatementEnd
ALTER TABLE secret_open_audit VALIDATE CONSTRAINT secret_open_audit_subject_check;
CREATE INDEX IF NOT EXISTS secret_open_audit_job_idx ON secret_open_audit (job_id, opened_at) WHERE job_id IS NOT NULL;

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP INDEX IF EXISTS secret_open_audit_job_idx;
ALTER TABLE secret_open_audit DROP CONSTRAINT IF EXISTS secret_open_audit_subject_check;
DELETE FROM secret_open_audit WHERE app_id IS NULL;
ALTER TABLE secret_open_audit ALTER COLUMN app_id SET NOT NULL;
ALTER TABLE secret_open_audit DROP COLUMN IF EXISTS job_id;
