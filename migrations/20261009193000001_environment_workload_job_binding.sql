-- Persist the Gregale Job that implements a scheduled GitOps workload. The
-- Job is created paused and becomes dispatchable only after the activation
-- path is qualified and explicitly opens it.
-- +goose Up
ALTER TABLE app_environment_workload_intents
    ADD COLUMN IF NOT EXISTS job_id uuid REFERENCES jobs(id) ON DELETE SET NULL;

CREATE UNIQUE INDEX IF NOT EXISTS app_environment_workload_intents_job_id_uniq
    ON app_environment_workload_intents(job_id) WHERE job_id IS NOT NULL;

-- A user-facing Jobs API may still read a GitOps-managed job, but it cannot
-- mutate settings owned by the reviewed environment definition. The active
-- reconciler lease is the only writer for those settings. Scheduler and image
-- materializer updates to their own operational columns remain allowed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_job() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
DECLARE
    managed boolean;
    controller boolean;
    deleting_account boolean;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM app_environment_workload_intents i
        WHERE i.job_id=OLD.id
    ) INTO managed;
    IF NOT managed THEN
        IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
    END IF;

    SELECT EXISTS (
        SELECT 1
        FROM app_environment_workload_intents i
        JOIN active_environment_git_sources s
          ON s.account_id=i.account_id AND s.environment_id=i.environment_id
        JOIN environment_gitops_resources r
          ON r.source_id=s.id AND r.app_id=i.app_id
        JOIN environment_gitops_jobs j ON j.source_id=s.id
        WHERE i.job_id=OLD.id AND s.mode='enforce' AND NOT s.suspended
          AND s.approved_revision_id IS NOT NULL
          AND j.desired_generation=s.generation AND j.claimed_generation=s.generation
          AND j.lease_until>clock_timestamp() AND j.lease_token<>''
          AND j.lease_token=current_setting('gregale.gitops_lease',true)
    ) INTO controller;
    SELECT EXISTS (
        SELECT 1 FROM accounts WHERE id=OLD.account_id AND status='deleted_pending'
    ) INTO deleting_account;

    IF TG_OP='DELETE' THEN
        IF NOT controller AND NOT deleting_account THEN
            RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
                MESSAGE='Job settings are managed by the environment Git source';
        END IF;
        RETURN OLD;
    END IF;

    IF ROW(NEW.account_id,NEW.kind,NEW.name,NEW.image_ref,NEW.ram_mb,NEW.task_timeout_s,
           NEW.max_parallelism,NEW.retry_max,NEW.env_overrides,NEW.status,NEW.command,
           NEW.cron_schedule,NEW.cron_timezone,NEW.schedule_policy,NEW.failure_rules)
       IS DISTINCT FROM
       ROW(OLD.account_id,OLD.kind,OLD.name,OLD.image_ref,OLD.ram_mb,OLD.task_timeout_s,
           OLD.max_parallelism,OLD.retry_max,OLD.env_overrides,OLD.status,OLD.command,
           OLD.cron_schedule,OLD.cron_timezone,OLD.schedule_policy,OLD.failure_rules)
       AND NOT controller AND NOT deleting_account THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
            MESSAGE='Job settings are managed by the environment Git source';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS guard_environment_workload_job ON jobs;
CREATE TRIGGER guard_environment_workload_job
    BEFORE UPDATE OR DELETE ON jobs
    FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_job();

-- The persisted binding is meaningful only when it points at the exact
-- immutable image and schedule in this scoped intent. Paused and active are
-- both valid here: only the separate activation transaction may open a job.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_job_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP<>'DELETE' AND NEW.job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM jobs j
        WHERE j.id=NEW.job_id AND j.account_id=NEW.account_id
          AND j.kind='recurring' AND j.status IN ('paused','active')
          AND NEW.schedule IS NOT NULL AND NEW.source->>'kind'='image'
          AND j.image_ref=NEW.source->>'image'
          AND j.env_overrides=NEW.variables
          AND j.cron_schedule=NEW.schedule->>'cron'
          AND j.cron_timezone=NEW.schedule->>'timezone'
          AND j.schedule_policy IS NOT DISTINCT FROM NULLIF(NEW.schedule->'schedule_policy','null'::jsonb)
          AND j.failure_rules IS NOT DISTINCT FROM NULLIF(NEW.schedule->'failure_rules','null'::jsonb)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_link_contract',
            MESSAGE='GitOps Job binding does not match the reviewed image and schedule';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS guard_environment_workload_job_link ON app_environment_workload_intents;
CREATE TRIGGER guard_environment_workload_job_link
    BEFORE INSERT OR UPDATE ON app_environment_workload_intents
    FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_job_link();

-- +goose Down
DROP TRIGGER guard_environment_workload_job_link ON app_environment_workload_intents;
DROP FUNCTION guard_environment_workload_job_link();
DROP TRIGGER guard_environment_workload_job ON jobs;
DROP FUNCTION guard_environment_workload_job();
DROP INDEX app_environment_workload_intents_job_id_uniq;
ALTER TABLE app_environment_workload_intents DROP COLUMN job_id;
