-- filename: 00589_instances_job_deployment_nullable.sql
-- +goose Up
-- +goose StatementBegin

-- Job definitions own their OCI image directly; a job-task instance is
-- linked by job_id and therefore has no deployment row to reference.
-- The original instances table made deployment_id unconditionally NOT NULL,
-- which made the jobs path impossible to persist without inventing a fake
-- deployment. Keep the app/deployment contract for wake/build rows and allow
-- the job-task shape introduced by 00256/00578 to use NULL.
ALTER TABLE instances
    ALTER COLUMN deployment_id DROP NOT NULL;

-- Keep the live-instance predicate consistent across the admission gate,
-- sampler, and job deletion path. Terminal rows remain in instances for
-- audit/retention, but must not block deletion or consume job concurrency.
CREATE OR REPLACE FUNCTION soft_delete_job_if_no_live_instances(p_job_id uuid)
    RETURNS boolean
    LANGUAGE plpgsql
AS $$
DECLARE
    flipped boolean;
BEGIN
    UPDATE jobs
       SET status = 'deleted', updated_at = now()
     WHERE id = p_job_id
       AND status <> 'deleted'
       AND NOT EXISTS (
           SELECT 1
             FROM instances
            WHERE job_id = p_job_id
              AND kind = 'job_task'
              AND state IN ('waking', 'cold_booting', 'running')
       )
    RETURNING TRUE INTO flipped;
    RETURN COALESCE(flipped, FALSE);
END;
$$;

DROP INDEX IF EXISTS instances_job_active_idx;
CREATE INDEX IF NOT EXISTS instances_job_active_idx
    ON instances (job_id)
    WHERE kind = 'job_task'
      AND state IN ('waking', 'cold_booting', 'running');

-- Forward-only: re-adding NOT NULL would reject valid job-task rows. The
-- job_id/kind pair CHECK remains the source of truth for job ownership.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
