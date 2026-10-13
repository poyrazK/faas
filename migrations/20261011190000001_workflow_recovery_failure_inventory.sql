-- +goose Up
-- +goose StatementBegin
-- The complete feature marker survives later migrations. Reconciliation must
-- preserve retained evidence and must not restore an earlier, narrower constraint.
DO $replay_guard$
BEGIN
 IF to_regclass('workflow_recovery_failure_capture') IS NOT NULL AND EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='workflow_runs'::regclass AND tgname='workflow_recovery_failure_evidence_capture' AND NOT tgisinternal) THEN
  RETURN;
 END IF;
-- Schema-enforced capture bounds mirror pkg/api/limits.go; checked by state tests.
CREATE TABLE workflow_recovery_failure_capture (
 singleton boolean PRIMARY KEY CHECK(singleton),
 started_at timestamptz NOT NULL CHECK(isfinite(started_at)),
 max_entries integer NOT NULL CHECK(max_entries>0),
 retention_seconds bigint NOT NULL CHECK(retention_seconds>0)
);
INSERT INTO workflow_recovery_failure_capture VALUES(true,clock_timestamp(),10000,2592000);
CREATE TABLE workflow_recovery_failure_evidence (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE CHECK(sequence>0),
 app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
 run_id uuid NOT NULL,
 workflow text NOT NULL CHECK(workflow<>''),
 resume_count integer NOT NULL CHECK(resume_count>=0),
 failure_status text NOT NULL CHECK(failure_status IN ('failed','dead')),
 observed_at timestamptz NOT NULL CHECK(isfinite(observed_at)),
 completed_at timestamptz CHECK(completed_at IS NULL OR isfinite(completed_at)),
 UNIQUE(app_id,run_id,resume_count),
 CHECK(completed_at IS NULL OR completed_at<=observed_at)
);
CREATE INDEX workflow_recovery_failure_evidence_app_time ON workflow_recovery_failure_evidence(app_id,observed_at,id);
CREATE INDEX workflow_recovery_failure_evidence_app_sequence ON workflow_recovery_failure_evidence(app_id,sequence DESC);
CREATE INDEX workflow_recovery_failure_evidence_observed ON workflow_recovery_failure_evidence(observed_at,id);

CREATE FUNCTION capture_workflow_recovery_failure(p_app uuid,p_run uuid,p_workflow text,p_status text,p_count integer,p_completed timestamptz,p_cancelled boolean)
RETURNS void LANGUAGE plpgsql AS $$
DECLARE observed timestamptz:=clock_timestamp(); retained integer; age_seconds bigint; inserted uuid;
BEGIN
 IF p_status NOT IN ('failed','dead') OR p_cancelled OR p_completed>observed THEN RETURN; END IF;
 IF NOT EXISTS(SELECT 1 FROM apps WHERE id=p_app AND status<>'deleted') THEN RETURN; END IF;
 -- Serialize retention and first-observation capture for this app only.
 PERFORM pg_advisory_xact_lock(hashtextextended('workflow-failure-evidence:'||p_app::text,0));
 SELECT max_entries,retention_seconds INTO retained,age_seconds FROM workflow_recovery_failure_capture WHERE singleton;
 INSERT INTO workflow_recovery_failure_evidence(id,app_id,run_id,workflow,resume_count,failure_status,observed_at,completed_at)
 VALUES(gen_random_uuid(),p_app,p_run,p_workflow,p_count,p_status,observed,p_completed)
 ON CONFLICT(app_id,run_id,resume_count) DO NOTHING RETURNING id INTO inserted;
 IF inserted IS NULL THEN RETURN; END IF;
 DELETE FROM workflow_recovery_failure_evidence WHERE app_id=p_app AND observed_at<=observed-make_interval(secs=>age_seconds::double precision);
 DELETE FROM workflow_recovery_failure_evidence WHERE app_id=p_app AND sequence < (
 SELECT sequence FROM workflow_recovery_failure_evidence WHERE app_id=p_app ORDER BY sequence DESC OFFSET retained-1 LIMIT 1);
END $$;
CREATE FUNCTION record_workflow_recovery_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='UPDATE' THEN
  IF OLD.status IN ('failed','dead','succeeded') AND OLD.resume_count=NEW.resume_count THEN RETURN NEW; END IF;
 END IF;
 PERFORM capture_workflow_recovery_failure(NEW.app_id,NEW.id,NEW.workflow_name,NEW.status,NEW.resume_count,NEW.finished_at,
 NEW.cancelled_at IS NOT NULL OR coalesce(NEW.last_error='cancelled by operator',false));
 RETURN NEW;
END $$;
CREATE TRIGGER workflow_recovery_failure_evidence_capture AFTER INSERT OR UPDATE OF status,resume_count ON workflow_runs
 FOR EACH ROW EXECUTE FUNCTION record_workflow_recovery_failure();
-- Prospective capture only: no inference or backfill from retained live rows.
END
$replay_guard$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_failure_evidence) THEN
 RAISE EXCEPTION 'cannot downgrade while policy-independent failure evidence is retained';
 END IF;
END $$;
DROP TRIGGER workflow_recovery_failure_evidence_capture ON workflow_runs;
DROP FUNCTION record_workflow_recovery_failure();
DROP FUNCTION capture_workflow_recovery_failure(uuid,uuid,text,text,integer,timestamptz,boolean);
DROP TABLE workflow_recovery_failure_evidence;
DROP TABLE workflow_recovery_failure_capture;
-- +goose StatementEnd
