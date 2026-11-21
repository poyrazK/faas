SET LOCAL lock_timeout = '5s';

-- Keep compact retired identities after owner-configured history retention.
-- Retrying an old UUID must never recreate a job whose full receipt expired.
CREATE SCHEMA IF NOT EXISTS gregale_job_maintenance;
REVOKE ALL ON SCHEMA gregale_job_maintenance FROM PUBLIC;
CREATE TABLE gregale_job_maintenance.retired_ids(id uuid PRIMARY KEY,retired_at timestamptz NOT NULL DEFAULT clock_timestamp());
REVOKE ALL ON gregale_job_maintenance.retired_ids FROM PUBLIC;
CREATE FUNCTION gregale_job_maintenance.guard_identity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtextextended('gregale:job-id:'||NEW.id,0));
 IF EXISTS(SELECT 1 FROM gregale_job_maintenance.retired_ids WHERE id=NEW.id) THEN
  RAISE SQLSTATE 'PT410' USING MESSAGE='job_history_retired';
 END IF;
 RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION gregale_job_maintenance.guard_identity() FROM PUBLIC;
CREATE TRIGGER team_order_job_identity BEFORE INSERT ON api.team_order_jobs
 FOR EACH ROW EXECUTE FUNCTION gregale_job_maintenance.guard_identity();
