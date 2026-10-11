-- +goose Up
-- +goose StatementBegin
-- Item-owned evidence survives workflow/run-history pruning.
CREATE TABLE event_recovery_workflow_results (
 job_id uuid NOT NULL,
 position bigint NOT NULL CHECK (position>0),
 run_id uuid NOT NULL,
 resume_number integer NOT NULL CHECK (resume_number>0),
 admitted_at timestamptz NOT NULL,
 state text NOT NULL CHECK (state IN ('succeeded','failed','cancelled','superseded')),
 completed_at timestamptz,
 recorded_at timestamptz NOT NULL,
 PRIMARY KEY (job_id,position),
 FOREIGN KEY (job_id,position) REFERENCES event_recovery_items(job_id,position) ON DELETE CASCADE,
 CHECK (recorded_at>=admitted_at),
 CHECK (completed_at IS NULL OR completed_at BETWEEN admitted_at AND recorded_at),
 CHECK (state<>'superseded' OR completed_at IS NULL)
);
CREATE INDEX event_recovery_items_workflow_identity_idx
 ON event_recovery_items (((expected_progress->>'run_id')::uuid))
 WHERE state='queued' AND expected_progress ? 'run_id';

CREATE FUNCTION capture_event_recovery_workflow_result(p_run uuid,p_app uuid,p_name text,p_count integer,
 p_status text,p_updated timestamptz,p_finished timestamptz,p_cancelled boolean)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE observed timestamptz := clock_timestamp();
BEGIN
 IF p_updated>observed THEN RETURN; END IF;
 INSERT INTO event_recovery_workflow_results(job_id,position,run_id,resume_number,admitted_at,state,completed_at,recorded_at)
 SELECT item.job_id,item.position,p_run,resume.resume_number,resume.created_at,
 CASE WHEN p_count>resume.resume_number THEN 'superseded'
 WHEN p_status='succeeded' THEN 'succeeded'
 WHEN p_cancelled THEN 'cancelled' ELSE 'failed' END,
 CASE WHEN p_count>resume.resume_number THEN NULL ELSE p_finished END,observed
 FROM event_recovery_items item JOIN event_recovery_jobs job ON job.id=item.job_id
 JOIN apps app ON app.id=job.app_id AND app.account_id=job.account_id
 JOIN workflow_run_resumes resume ON resume.run_id=p_run AND resume.account_id=job.account_id
  AND resume.resume_number=(item.expected_progress->>'expected_resume_count')::integer+1
  AND resume.created_at>=job.created_at AND resume.created_at<=observed
 WHERE item.state='queued' AND job.selection->>'mode'='workflow' AND job.app_id=p_app
  AND job.selection->'workflow_runs'->>'name'=p_name
  AND (item.expected_progress->>'run_id')::uuid=p_run
  AND p_count>=resume.resume_number AND p_updated>=resume.created_at
  AND (p_count>resume.resume_number OR p_status IN ('succeeded','failed','dead'))
  AND (p_count>resume.resume_number OR p_finished IS NULL OR p_finished BETWEEN resume.created_at AND observed)
 ORDER BY item.job_id,item.position
 FOR KEY SHARE OF item
 ON CONFLICT (job_id,position) DO NOTHING;
END $$;

CREATE FUNCTION record_event_recovery_workflow_result() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP IN ('DELETE','UPDATE') THEN
  PERFORM capture_event_recovery_workflow_result(OLD.id,OLD.app_id,OLD.workflow_name,OLD.resume_count,
   OLD.status,OLD.updated_at,OLD.finished_at,OLD.cancelled_at IS NOT NULL OR coalesce(OLD.last_error='cancelled by operator',false));
 END IF;
 IF TG_OP<>'DELETE' THEN
  PERFORM capture_event_recovery_workflow_result(NEW.id,NEW.app_id,NEW.workflow_name,NEW.resume_count,
   NEW.status,NEW.updated_at,NEW.finished_at,NEW.cancelled_at IS NOT NULL OR coalesce(NEW.last_error='cancelled by operator',false));
  RETURN NEW;
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER event_recovery_workflow_result
 BEFORE DELETE OR UPDATE OF status,resume_count,finished_at,cancelled_at,last_error ON workflow_runs
 FOR EACH ROW EXECUTE FUNCTION record_event_recovery_workflow_result();

-- Backfill retained evidence only; do not infer outcomes from missing history.
SELECT capture_event_recovery_workflow_result(r.id,r.app_id,r.workflow_name,r.resume_count,r.status,
 r.updated_at,r.finished_at,r.cancelled_at IS NOT NULL OR coalesce(r.last_error='cancelled by operator',false))
FROM workflow_runs r WHERE EXISTS (
 SELECT 1 FROM event_recovery_items i JOIN event_recovery_jobs j ON j.id=i.job_id
 WHERE i.state='queued' AND j.selection->>'mode'='workflow' AND j.app_id=r.app_id
 AND (i.expected_progress->>'run_id')::uuid=r.id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS event_recovery_workflow_result ON workflow_runs;
DROP FUNCTION IF EXISTS record_event_recovery_workflow_result();
DROP FUNCTION IF EXISTS capture_event_recovery_workflow_result(uuid,uuid,text,integer,text,timestamptz,timestamptz,boolean);
DROP INDEX IF EXISTS event_recovery_items_workflow_identity_idx;
DROP TABLE IF EXISTS event_recovery_workflow_results;
-- +goose StatementEnd
