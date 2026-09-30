-- +goose Up
-- Explicit policy objects opt a definition into the versioned execution contract.
ALTER TABLE jobs
 ADD COLUMN IF NOT EXISTS schedule_policy jsonb,
 ADD COLUMN IF NOT EXISTS failure_rules jsonb,
 ADD COLUMN IF NOT EXISTS schedule_revision bigint NOT NULL DEFAULT 1 CHECK (schedule_revision >= 1);
ALTER TABLE crons
 ADD COLUMN IF NOT EXISTS schedule_policy jsonb,
 ADD COLUMN IF NOT EXISTS failure_rules jsonb,
 ADD COLUMN IF NOT EXISTS schedule_revision bigint NOT NULL DEFAULT 1 CHECK (schedule_revision >= 1);

-- Constraint creation is guarded separately because PostgreSQL does not
-- support ADD CONSTRAINT IF NOT EXISTS.
-- +goose StatementBegin
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conname = 'jobs_schedule_policy_shape' AND conrelid = 'jobs'::regclass) THEN
  ALTER TABLE jobs ADD CONSTRAINT jobs_schedule_policy_shape CHECK (schedule_policy IS NULL OR (jsonb_typeof(schedule_policy) = 'object' AND schedule_policy->>'version' = '1'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conname = 'jobs_failure_rules_shape' AND conrelid = 'jobs'::regclass) THEN
  ALTER TABLE jobs ADD CONSTRAINT jobs_failure_rules_shape CHECK (failure_rules IS NULL OR (jsonb_typeof(failure_rules) = 'object' AND failure_rules->>'version' = '1'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conname = 'crons_schedule_policy_shape' AND conrelid = 'crons'::regclass) THEN
  ALTER TABLE crons ADD CONSTRAINT crons_schedule_policy_shape CHECK (schedule_policy IS NULL OR (jsonb_typeof(schedule_policy) = 'object' AND schedule_policy->>'version' = '1'));
 END IF;
 IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conname = 'crons_failure_rules_shape' AND conrelid = 'crons'::regclass) THEN
  ALTER TABLE crons ADD CONSTRAINT crons_failure_rules_shape CHECK (failure_rules IS NULL OR (jsonb_typeof(failure_rules) = 'object' AND failure_rules->>'version' = '1'));
 END IF;
END $$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS schedule_occurrences (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 cron_id uuid REFERENCES crons(id) ON DELETE CASCADE,
 job_id uuid REFERENCES jobs(id) ON DELETE CASCADE,
 schedule_revision bigint NOT NULL CHECK (schedule_revision >= 1),
 scheduled_for timestamptz NOT NULL,
 start_deadline_at timestamptz,
 schedule_policy jsonb NOT NULL CHECK (jsonb_typeof(schedule_policy) = 'object' AND schedule_policy->>'version' = '1'),
 status text NOT NULL CHECK (status IN ('pending','queued','running','succeeded','failed','cancelled','skipped_overlap','missed_deadline','coalesced','waiting_replacement','uncertain')),
 reason text NOT NULL DEFAULT '' CHECK (octet_length(reason) <= 4096),
 blocking_occurrence_id uuid REFERENCES schedule_occurrences(id) ON DELETE SET NULL,
 invocation_id uuid REFERENCES invocations(id) ON DELETE SET NULL,
 app_task_id uuid REFERENCES app_tasks(id) ON DELETE SET NULL,
 job_run_id uuid REFERENCES job_runs(id) ON DELETE SET NULL,
 started_at timestamptz,
 finished_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK ((cron_id IS NOT NULL)::int + (job_id IS NOT NULL)::int = 1),
 CHECK (start_deadline_at IS NULL OR start_deadline_at >= scheduled_for)
);
CREATE UNIQUE INDEX IF NOT EXISTS schedule_occurrences_cron_identity ON schedule_occurrences(cron_id,schedule_revision,scheduled_for) WHERE cron_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS schedule_occurrences_job_identity ON schedule_occurrences(job_id,schedule_revision,scheduled_for) WHERE job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS schedule_occurrences_account_history ON schedule_occurrences(account_id,scheduled_for DESC,id DESC);
CREATE INDEX IF NOT EXISTS schedule_occurrences_job_history ON schedule_occurrences(job_id,scheduled_for DESC,id DESC) WHERE job_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS schedule_occurrences_cron_history ON schedule_occurrences(cron_id,scheduled_for DESC,id DESC) WHERE cron_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS schedule_occurrences_pending ON schedule_occurrences(status,scheduled_for,id) WHERE status IN ('pending','waiting_replacement');

ALTER TABLE job_runs ADD COLUMN IF NOT EXISTS failure_rules jsonb,
 ADD COLUMN IF NOT EXISTS occurrence_id uuid REFERENCES schedule_occurrences(id) ON DELETE SET NULL,
 ADD COLUMN IF NOT EXISTS start_deadline_at timestamptz;
ALTER TABLE job_tasks ADD COLUMN IF NOT EXISTS work_decision jsonb,
 ADD COLUMN IF NOT EXISTS outcome_code text NOT NULL DEFAULT '' CHECK (octet_length(outcome_code) <= 64);
ALTER TABLE job_task_attempts ADD COLUMN IF NOT EXISTS work_decision jsonb,
 ADD COLUMN IF NOT EXISTS outcome_code text NOT NULL DEFAULT '' CHECK (octet_length(outcome_code) <= 64);
ALTER TABLE app_tasks ADD COLUMN IF NOT EXISTS failure_rules jsonb,
 ADD COLUMN IF NOT EXISTS occurrence_id uuid REFERENCES schedule_occurrences(id) ON DELETE SET NULL,
 ADD COLUMN IF NOT EXISTS start_deadline_at timestamptz,
 ADD COLUMN IF NOT EXISTS work_decision jsonb,
 ADD COLUMN IF NOT EXISTS outcome_code text NOT NULL DEFAULT '' CHECK (octet_length(outcome_code) <= 64);
ALTER TABLE invocations ADD COLUMN IF NOT EXISTS failure_rules jsonb,
 ADD COLUMN IF NOT EXISTS occurrence_id uuid REFERENCES schedule_occurrences(id) ON DELETE SET NULL,
 ADD COLUMN IF NOT EXISTS start_deadline_at timestamptz,
 ADD COLUMN IF NOT EXISTS work_decision jsonb;

-- The reaper records lost completion receipts as an explicit uncertain class.
ALTER TABLE job_tasks
    DROP CONSTRAINT IF EXISTS job_tasks_error_class_check;
ALTER TABLE job_tasks
    ADD CONSTRAINT job_tasks_error_class_check
    CHECK (
        error_class IS NULL
        OR error_class IN (
            'timeout', 'refused', 'tls_handshake', 'dns', 'unreachable', 'oom',
            'user_error', 'infra', 'success', 'succeeded', 'failed', 'cancelled',
            'job_paused', 'oom_or_killed', 'uncertain'
        )
    );

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION revise_job_schedule_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.cron_schedule IS DISTINCT FROM OLD.cron_schedule OR NEW.cron_timezone IS DISTINCT FROM OLD.cron_timezone OR NEW.schedule_policy IS DISTINCT FROM OLD.schedule_policy THEN
  NEW.schedule_revision := OLD.schedule_revision + 1;
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS jobs_schedule_revision ON jobs;
CREATE TRIGGER jobs_schedule_revision BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION revise_job_schedule_policy();
CREATE OR REPLACE FUNCTION revise_cron_schedule_policy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.schedule IS DISTINCT FROM OLD.schedule OR NEW.timezone IS DISTINCT FROM OLD.timezone OR NEW.schedule_policy IS DISTINCT FROM OLD.schedule_policy THEN
  NEW.schedule_revision := OLD.schedule_revision + 1;
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS crons_schedule_revision ON crons;
CREATE TRIGGER crons_schedule_revision BEFORE UPDATE ON crons FOR EACH ROW EXECUTE FUNCTION revise_cron_schedule_policy();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_job_schedule_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
 first_started timestamptz;
 missed_deadline boolean;
BEGIN
 IF NEW.aggregate_status IS NOT DISTINCT FROM OLD.aggregate_status THEN RETURN NEW; END IF;
 SELECT min(started_at) INTO first_started FROM job_tasks WHERE run_id = NEW.id AND started_at IS NOT NULL;
 SELECT o.start_deadline_at IS NOT NULL
        AND o.start_deadline_at < COALESCE(NEW.finished_at, clock_timestamp())
        AND first_started IS NULL
   INTO missed_deadline
   FROM schedule_occurrences o WHERE o.job_run_id = NEW.id;
 UPDATE schedule_occurrences o SET
   status = CASE
     WHEN COALESCE(missed_deadline, false) THEN 'missed_deadline'
     WHEN NEW.aggregate_status = 'queued' THEN 'queued'
     WHEN NEW.aggregate_status = 'running' THEN 'running'
     WHEN NEW.aggregate_status = 'succeeded' THEN 'succeeded'
     WHEN NEW.aggregate_status = 'cancelled' THEN 'cancelled'
     ELSE 'failed' END,
   reason = CASE WHEN COALESCE(missed_deadline, false)
       THEN 'no task started before the occurrence start deadline' ELSE o.reason END,
   started_at = COALESCE(o.started_at, first_started),
   finished_at = CASE WHEN NEW.aggregate_status IN ('succeeded','failed','cancelled','dead_letter')
       OR COALESCE(missed_deadline, false) THEN COALESCE(NEW.finished_at, clock_timestamp()) ELSE NULL END,
   updated_at = clock_timestamp()
 WHERE o.job_run_id = NEW.id;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS job_runs_schedule_occurrence ON job_runs;
CREATE TRIGGER job_runs_schedule_occurrence AFTER UPDATE OF aggregate_status ON job_runs
 FOR EACH ROW EXECUTE FUNCTION sync_job_schedule_occurrence();

CREATE OR REPLACE FUNCTION sync_app_task_schedule_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.status IS NOT DISTINCT FROM OLD.status THEN RETURN NEW; END IF;
 UPDATE schedule_occurrences o SET
   status = CASE WHEN NEW.start_deadline_at IS NOT NULL AND NEW.started_at IS NULL AND o.started_at IS NULL
       AND NEW.start_deadline_at < COALESCE(NEW.finished_at, clock_timestamp()) THEN 'missed_deadline'
     ELSE CASE NEW.status
       WHEN 'queued' THEN CASE WHEN o.started_at IS NULL THEN 'queued' ELSE 'running' END
       WHEN 'restoring' THEN 'running' WHEN 'running' THEN 'running'
       WHEN 'succeeded' THEN 'succeeded' WHEN 'failed' THEN 'failed'
       WHEN 'timed_out' THEN 'failed' WHEN 'cancelled' THEN 'cancelled' ELSE o.status END END,
   reason = CASE WHEN NEW.start_deadline_at IS NOT NULL AND NEW.started_at IS NULL AND o.started_at IS NULL
       AND NEW.start_deadline_at < COALESCE(NEW.finished_at, clock_timestamp())
       THEN 'command task did not start before the occurrence start deadline' ELSE o.reason END,
   started_at = COALESCE(o.started_at, NEW.started_at),
   finished_at = CASE WHEN NEW.status IN ('succeeded','failed','timed_out','cancelled')
       THEN COALESCE(NEW.finished_at, clock_timestamp()) ELSE NULL END,
   updated_at = clock_timestamp()
 WHERE o.id = NEW.occurrence_id;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS app_tasks_schedule_occurrence ON app_tasks;
CREATE TRIGGER app_tasks_schedule_occurrence AFTER UPDATE OF status ON app_tasks
 FOR EACH ROW EXECUTE FUNCTION sync_app_task_schedule_occurrence();

CREATE OR REPLACE FUNCTION sync_invocation_schedule_occurrence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.state IS NOT DISTINCT FROM OLD.state THEN RETURN NEW; END IF;
 UPDATE schedule_occurrences o SET
   status = CASE NEW.state
     WHEN 'pending' THEN 'queued' WHEN 'dispatching' THEN 'running'
     WHEN 'completed' THEN 'succeeded' WHEN 'failed' THEN 'failed'
     WHEN 'cancelled' THEN 'cancelled' WHEN 'dead_letter' THEN 'failed' ELSE o.status END,
   reason = CASE WHEN o.started_at IS NULL AND NEW.start_deadline_at IS NOT NULL
       AND NEW.start_deadline_at < COALESCE(NEW.completed_at, clock_timestamp())
       AND NEW.state = 'failed' THEN 'invocation did not start before the occurrence start deadline' ELSE o.reason END,
   started_at = CASE WHEN NEW.state = 'dispatching' THEN COALESCE(o.started_at, clock_timestamp()) ELSE o.started_at END,
   finished_at = CASE WHEN NEW.state IN ('completed','failed','cancelled','dead_letter') THEN COALESCE(NEW.completed_at, clock_timestamp()) ELSE NULL END,
   updated_at = clock_timestamp()
 WHERE o.id = NEW.occurrence_id;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS invocations_schedule_occurrence ON invocations;
CREATE TRIGGER invocations_schedule_occurrence AFTER UPDATE OF state ON invocations
 FOR EACH ROW EXECUTE FUNCTION sync_invocation_schedule_occurrence();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION record_job_task_attempt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.attempt > OLD.attempt THEN
        -- A claimed VM may fail before a terminal task row is written. Its
        -- retry transition still consumes an attempt and needs a result.
        INSERT INTO job_task_attempts (
            run_id, task_index, attempt, status, instance_id, error_class,
            error_message, exit_code, started_at, finished_at, log_content,
            log_truncated, output_manifest, work_decision, outcome_code)
        VALUES (
            OLD.run_id, OLD.task_index, OLD.attempt,
            CASE WHEN OLD.status = 'claimed' THEN
                CASE WHEN NEW.error_class = 'infra' THEN 'failed' ELSE 'timeout' END
                ELSE OLD.status END,
            OLD.instance_id,
            CASE WHEN OLD.status = 'claimed' THEN COALESCE(NEW.error_class, 'infra') ELSE OLD.error_class END,
            CASE WHEN OLD.status = 'claimed' THEN
                COALESCE(NEW.error_message, 'reaper reclaimed stale lease')
                ELSE OLD.error_message END,
            CASE WHEN OLD.status = 'claimed' THEN
                CASE WHEN NEW.error_class = 'infra' THEN 1 ELSE 124 END
                ELSE OLD.exit_code END,
            OLD.started_at, COALESCE(OLD.finished_at, clock_timestamp()),
            OLD.log_content, OLD.log_truncated, OLD.output_manifest,
            COALESCE(NEW.work_decision, OLD.work_decision), COALESCE(NEW.outcome_code, OLD.outcome_code))
        ON CONFLICT (run_id, task_index, attempt) DO NOTHING;
    ELSIF NEW.status IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom')
       AND OLD.status NOT IN ('succeeded', 'failed', 'timeout', 'cancelled', 'oom') THEN
        INSERT INTO job_task_attempts (
            run_id, task_index, attempt, status, instance_id, error_class,
            error_message, exit_code, started_at, finished_at, log_content,
            log_truncated, output_manifest, work_decision, outcome_code)
        VALUES (
            NEW.run_id, NEW.task_index, NEW.attempt, NEW.status, NEW.instance_id,
            NEW.error_class, NEW.error_message, NEW.exit_code, NEW.started_at,
            COALESCE(NEW.finished_at, clock_timestamp()), NEW.log_content,
            NEW.log_truncated, NEW.output_manifest, NEW.work_decision, NEW.outcome_code)
        ON CONFLICT (run_id, task_index, attempt) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Forward-only: immutable policies and occurrence/attempt evidence must survive rollback.
SELECT 1;
