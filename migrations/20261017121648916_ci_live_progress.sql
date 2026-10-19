-- filename: 20261017121648916_ci_live_progress.sql

-- +goose Up
ALTER TABLE ci_job_attempts ADD COLUMN IF NOT EXISTS progress jsonb
 CHECK(progress IS NULL OR (jsonb_typeof(progress)='object' AND octet_length(progress::text)<=131072));
ALTER TABLE ci_job_attempts ADD COLUMN IF NOT EXISTS progress_sequence integer NOT NULL DEFAULT 0
 CHECK(progress_sequence BETWEEN 0 AND 4096);
ALTER TABLE ci_job_attempts ADD COLUMN IF NOT EXISTS progress_digest text NOT NULL DEFAULT ''
 CHECK(progress_digest='' OR progress_digest ~ '^[0-9a-f]{64}$');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM ci_job_attempts WHERE progress IS NOT NULL OR progress_sequence>0) THEN
  RAISE EXCEPTION 'retain CI live progress and sequence evidence before rollback';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE ci_job_attempts DROP COLUMN progress;
ALTER TABLE ci_job_attempts DROP COLUMN progress_sequence;
ALTER TABLE ci_job_attempts DROP COLUMN progress_digest;
