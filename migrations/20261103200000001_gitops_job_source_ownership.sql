-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
ALTER TABLE jobs ADD COLUMN environment_git_source_id uuid;
ALTER TABLE jobs ADD CONSTRAINT jobs_environment_git_source_fk
 FOREIGN KEY (environment_git_source_id) REFERENCES environment_git_sources(id) NOT VALID;

-- Missing attribution remains protected for N-1 writers. Current controllers
-- stamp the exact source; detach attributes legacy links in its transaction.
-- +goose StatementBegin
CREATE FUNCTION environment_workload_job_released(j jobs) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN j.environment_git_source_id IS NOT NULL THEN EXISTS (
  SELECT 1 FROM environment_git_sources s WHERE s.id=j.environment_git_source_id AND s.detached
 ) ELSE EXISTS (
  SELECT 1 FROM app_environment_workload_intents i
  JOIN environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
  JOIN environment_gitops_resources r ON r.source_id=s.id AND r.app_id=i.app_id
  WHERE i.job_id=j.id AND i.account_id=j.account_id AND s.detached AND j.created_at<=s.updated_at
   AND NOT EXISTS(SELECT 1 FROM active_environment_git_sources active WHERE active.environment_id=s.environment_id)
 ) END;
$$;
CREATE FUNCTION environment_workload_job_owned(j jobs) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT NOT environment_workload_job_released(j) AND (j.environment_git_source_id IS NOT NULL
  OR EXISTS(SELECT 1 FROM app_environment_workload_intents i WHERE i.job_id=j.id));
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_job() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE controller_source uuid; deleting_account boolean; allowed boolean;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.environment_git_source_id IS NOT NULL AND NOT EXISTS (
   SELECT 1 FROM active_environment_git_sources s JOIN environment_gitops_jobs work ON work.source_id=s.id
   WHERE s.id=NEW.environment_git_source_id AND s.account_id=NEW.account_id
    AND s.mode='enforce' AND NOT s.suspended AND s.approved_revision_id IS NOT NULL
    AND work.desired_generation=s.generation AND work.claimed_generation=s.generation
    AND work.lease_until>clock_timestamp() AND work.lease_token<>''
    AND work.lease_token=current_setting('gregale.gitops_lease',true)
  ) THEN
   RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
    MESSAGE='Job ownership requires the environment controller lease';
  END IF;
  RETURN NEW;
 END IF;
 SELECT s.id INTO controller_source
 FROM app_environment_workload_intents i
 JOIN active_environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
 JOIN environment_gitops_resources r ON r.source_id=s.id AND r.app_id=i.app_id
 JOIN environment_gitops_jobs work ON work.source_id=s.id
 WHERE i.job_id=OLD.id AND s.mode='enforce' AND NOT s.suspended AND s.approved_revision_id IS NOT NULL
  AND work.desired_generation=s.generation AND work.claimed_generation=s.generation
  AND work.lease_until>clock_timestamp() AND work.lease_token<>''
  AND work.lease_token=current_setting('gregale.gitops_lease',true);

 IF TG_OP='UPDATE' AND NEW.environment_git_source_id IS DISTINCT FROM OLD.environment_git_source_id THEN
  allowed:=NEW.environment_git_source_id IS NOT NULL AND NEW.environment_git_source_id=controller_source;
  -- Only the just-retired binding can attribute an unmarked legacy link.
  allowed:=coalesce(allowed,false) OR (OLD.environment_git_source_id IS NULL AND EXISTS (
   SELECT 1 FROM environment_git_sources s
   JOIN environment_gitops_resources r ON r.source_id=s.id
   JOIN app_environment_workload_intents i ON i.app_id=r.app_id AND i.environment_id=s.environment_id AND i.account_id=s.account_id
   WHERE s.id=NEW.environment_git_source_id AND s.detached AND i.job_id=OLD.id AND OLD.account_id=s.account_id
    AND NOT EXISTS(SELECT 1 FROM active_environment_git_sources active WHERE active.environment_id=s.environment_id)
  ));
  IF NOT allowed THEN
   RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
    MESSAGE='Job ownership requires the original source or controller lease';
  END IF;
 END IF;
 IF NOT environment_workload_job_owned(OLD) THEN
  IF TG_OP='DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
 END IF;
 SELECT EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id AND status='deleted_pending') INTO deleting_account;
 IF TG_OP='DELETE' THEN
  IF controller_source IS NULL AND NOT deleting_account THEN
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
  AND controller_source IS NULL AND NOT deleting_account THEN
  RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
   MESSAGE='Job settings are managed by the environment Git source';
 END IF;
 RETURN NEW;
END;
$$;
CREATE OR REPLACE TRIGGER guard_environment_workload_job BEFORE INSERT OR UPDATE OR DELETE ON jobs
 FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_job();
-- +goose StatementEnd

-- A detached source layer remains frozen. Changing the ordinary Job to an OCI
-- image may clear its old materialization; changing a command/schedule does
-- not grant authority to replace the source artifact.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_source_job_artifact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF environment_workload_job_released(OLD) AND (
  NEW.image_ref NOT LIKE 'gregale-source://%' OR
  ROW(NEW.image_resolved_digest,NEW.image_storage_key,NEW.image_materialization_status,NEW.image_materialization_attempts)
   IS NOT DISTINCT FROM ROW(OLD.image_resolved_digest,OLD.image_storage_key,OLD.image_materialization_status,OLD.image_materialization_attempts)
 ) THEN RETURN NEW; END IF;
 IF (OLD.image_ref LIKE 'gregale-source://%' OR NEW.image_ref LIKE 'gregale-source://%') AND
  ROW(NEW.image_resolved_digest,NEW.image_storage_key,NEW.image_materialization_status,NEW.image_materialization_attempts,NEW.schedule_revision)
   IS DISTINCT FROM ROW(OLD.image_resolved_digest,OLD.image_storage_key,OLD.image_materialization_status,OLD.image_materialization_attempts,OLD.schedule_revision)
  AND NOT EXISTS (
   SELECT 1 FROM app_environment_workload_intents i
   JOIN active_environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
   JOIN environment_gitops_jobs work ON work.source_id=s.id
   WHERE i.job_id=OLD.id AND s.mode='enforce' AND NOT s.suspended
    AND work.desired_generation=s.generation AND work.claimed_generation=s.generation
    AND work.lease_until>clock_timestamp() AND work.lease_token<>''
    AND work.lease_token=current_setting('gregale.gitops_lease',true)
  ) THEN
  RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
   MESSAGE='Source Job artifacts require the environment controller lease';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_job_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
 -- A released link is retained history until the replacement controller
 -- reconciles the Job. It carries no scheduler/runtime authority.
 IF TG_OP<>'DELETE' AND NEW.job_id IS NOT NULL AND EXISTS (
  SELECT 1 FROM jobs j WHERE j.id=NEW.job_id AND j.account_id=NEW.account_id AND environment_workload_job_released(j)
 ) THEN RETURN NEW; END IF;
    IF TG_OP<>'DELETE' AND NEW.job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM jobs j
        WHERE j.id=NEW.job_id AND j.account_id=NEW.account_id
          AND j.kind='recurring' AND j.status IN ('paused','active')
          AND NEW.schedule IS NOT NULL AND (
              (NEW.source->>'kind'='image' AND j.image_ref=NEW.source->>'image') OR
              (NEW.source->>'kind' IN ('source','dockerfile')
               AND j.image_ref=environment_workload_source_job_reference(NEW.environment_id,NEW.app_id,NEW.source,NEW.source_revision))
          )
          AND to_jsonb(j.command)=coalesce(nullif(NEW.runtime->'entrypoint','null'::jsonb),'[]'::jsonb)
          AND j.env_overrides=NEW.variables
          AND j.cron_schedule=NEW.schedule->>'cron'
          AND j.cron_timezone=NEW.schedule->>'timezone'
          AND j.schedule_policy IS NOT DISTINCT FROM NULLIF(NEW.schedule->'schedule_policy','null'::jsonb)
          AND j.failure_rules IS NOT DISTINCT FROM NULLIF(NEW.schedule->'failure_rules','null'::jsonb)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_link_contract',
            MESSAGE='GitOps Job binding does not match the reviewed source and schedule';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
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
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_source_job_artifact() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.image_ref LIKE 'gregale-source://%' OR NEW.image_ref LIKE 'gregale-source://%') AND
       ROW(NEW.image_resolved_digest,NEW.image_storage_key,NEW.image_materialization_status,
           NEW.image_materialization_attempts,NEW.schedule_revision)
       IS DISTINCT FROM ROW(OLD.image_resolved_digest,OLD.image_storage_key,OLD.image_materialization_status,
           OLD.image_materialization_attempts,OLD.schedule_revision) AND NOT EXISTS (
        SELECT 1 FROM app_environment_workload_intents i
        JOIN active_environment_git_sources s ON s.account_id=i.account_id AND s.environment_id=i.environment_id
        JOIN environment_gitops_jobs work ON work.source_id=s.id
        WHERE i.job_id=OLD.id AND s.mode='enforce' AND NOT s.suspended
            AND work.desired_generation=s.generation AND work.claimed_generation=s.generation
            AND work.lease_until>clock_timestamp() AND work.lease_token<>''
            AND work.lease_token=current_setting('gregale.gitops_lease',true)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_managed',
            MESSAGE='Source Job artifacts require the environment controller lease';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_job_link() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF TG_OP<>'DELETE' AND NEW.job_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM jobs j
        WHERE j.id=NEW.job_id AND j.account_id=NEW.account_id
          AND j.kind='recurring' AND j.status IN ('paused','active')
          AND NEW.schedule IS NOT NULL AND (
              (NEW.source->>'kind'='image' AND j.image_ref=NEW.source->>'image') OR
              (NEW.source->>'kind' IN ('source','dockerfile')
               AND j.image_ref=environment_workload_source_job_reference(NEW.environment_id,NEW.app_id,NEW.source,NEW.source_revision))
          )
          AND to_jsonb(j.command)=coalesce(nullif(NEW.runtime->'entrypoint','null'::jsonb),'[]'::jsonb)
          AND j.env_overrides=NEW.variables
          AND j.cron_schedule=NEW.schedule->>'cron'
          AND j.cron_timezone=NEW.schedule->>'timezone'
          AND j.schedule_policy IS NOT DISTINCT FROM NULLIF(NEW.schedule->'schedule_policy','null'::jsonb)
          AND j.failure_rules IS NOT DISTINCT FROM NULLIF(NEW.schedule->'failure_rules','null'::jsonb)
    ) THEN
        RAISE EXCEPTION USING ERRCODE='23514', CONSTRAINT='environment_gitops_job_link_contract',
            MESSAGE='GitOps Job binding does not match the reviewed source and schedule';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER guard_environment_workload_job ON jobs;
CREATE TRIGGER guard_environment_workload_job BEFORE UPDATE OR DELETE ON jobs
 FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_job();
DROP FUNCTION environment_workload_job_owned(jobs);
DROP FUNCTION IF EXISTS environment_workload_job_released(jobs);
ALTER TABLE jobs DROP COLUMN environment_git_source_id;
