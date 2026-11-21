-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';
CREATE TABLE execution_workflow_pipelines (
 job_id uuid PRIMARY KEY REFERENCES agent_execution_workflows(id) ON DELETE CASCADE,
 policy jsonb NOT NULL CHECK (jsonb_typeof(policy)='object' AND octet_length(policy::text)<=65536),
 deadline_at timestamptz NOT NULL,
 due_at timestamptz NOT NULL,
 cancel_requested boolean NOT NULL DEFAULT false
);
CREATE INDEX execution_pipeline_due_idx ON execution_workflow_pipelines(due_at,job_id);
-- +goose StatementBegin
CREATE FUNCTION execution_pipeline_writer_fence() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.step_label LIKE 'gwf:p2:%' AND current_setting('faas.run_pipeline_protocol',true) IS DISTINCT FROM '1' THEN
  RAISE EXCEPTION 'pipeline admission protocol required' USING ERRCODE='check_violation';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER execution_pipeline_writer_fence BEFORE INSERT ON executions FOR EACH ROW EXECUTE FUNCTION execution_pipeline_writer_fence();
-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TRIGGER execution_pipeline_writer_fence ON executions;
DROP FUNCTION execution_pipeline_writer_fence();
DROP TABLE execution_workflow_pipelines;
