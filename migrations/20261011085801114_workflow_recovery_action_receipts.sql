-- +goose Up
CREATE TABLE workflow_recovery_action_receipts (
 job_id uuid NOT NULL REFERENCES event_recovery_jobs(id) ON DELETE CASCADE,
 request_id uuid NOT NULL,
 request jsonb NOT NULL CHECK(jsonb_typeof(request)='object'),
 response jsonb NOT NULL CHECK(jsonb_typeof(response)='object'),
 captured_at timestamptz NOT NULL,
 actor_kind text NOT NULL,
 actor_id text NOT NULL,
 PRIMARY KEY(job_id,request_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM workflow_recovery_action_receipts) THEN RAISE EXCEPTION 'cannot downgrade while recovery action receipts are retained'; END IF;
END $$;
DROP TABLE workflow_recovery_action_receipts;
-- +goose StatementEnd
