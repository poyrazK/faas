-- +goose Up
-- Fresh, explicit Jobs executions only. Blocked ci_runs are never converted.
ALTER TABLE ci_executions DROP CONSTRAINT IF EXISTS ci_executions_mode_check;
ALTER TABLE ci_executions ADD CONSTRAINT ci_executions_mode_check CHECK (mode IN ('qualification', 'jobs'));
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'ci_executions'::regclass AND conname = 'ci_executions_compute_check') THEN
  ALTER TABLE ci_executions ADD CONSTRAINT ci_executions_compute_check CHECK (
   (mode = 'jobs' AND snapshot->'compute' IS NOT NULL AND jsonb_typeof(snapshot->'compute') = 'object')
   OR (mode = 'qualification' AND (snapshot->'compute' IS NULL OR snapshot->'compute' = 'null'::jsonb))
  );
 END IF;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS ci_job_attempts (
 attempt_id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 execution_id uuid NOT NULL,
 job_name text NOT NULL CHECK (job_name ~ '^[a-z][a-z0-9-]{0,39}$'),
 fence bigint NOT NULL CHECK (fence > 0),
 cancel_requested boolean NOT NULL DEFAULT false,
 run_id uuid UNIQUE REFERENCES job_runs(id),
 source jsonb CHECK (source IS NULL OR (jsonb_typeof(source) = 'object' AND octet_length(source::text) <= 16384)),
 output jsonb CHECK (output IS NULL OR (jsonb_typeof(output) = 'object' AND octet_length(output::text) <= 262144)),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY (account_id, execution_id) REFERENCES ci_executions(account_id, id) ON DELETE CASCADE,
 UNIQUE (execution_id, job_name),
 CHECK ((run_id IS NULL AND source IS NULL) OR (run_id IS NOT NULL AND source IS NOT NULL)),
 CHECK (run_id IS NULL OR run_id = attempt_id),
 CHECK (output IS NULL OR run_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS ci_job_attempts_account_execution_idx ON ci_job_attempts(account_id, execution_id);

-- +goose Down
-- Do not silently discard the only recovery authority for a live Jobs run.
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM ci_executions WHERE mode = 'jobs') THEN
  RAISE EXCEPTION 'CI Jobs executions must be explicitly reconciled and removed before rollback';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE ci_job_attempts;
ALTER TABLE ci_executions DROP CONSTRAINT ci_executions_compute_check;
ALTER TABLE ci_executions DROP CONSTRAINT ci_executions_mode_check;
ALTER TABLE ci_executions ADD CONSTRAINT ci_executions_mode_check CHECK (mode = 'qualification');
