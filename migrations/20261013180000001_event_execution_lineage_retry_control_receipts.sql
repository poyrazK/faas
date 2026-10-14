-- +goose Up
CREATE TABLE IF NOT EXISTS event_execution_lineage_retry_control_receipts (
 subscription_id uuid NOT NULL,
 job_request_id uuid NOT NULL,
 command_id uuid NOT NULL CHECK(command_id<>'00000000-0000-0000-0000-000000000000'::uuid),
 account_id uuid NOT NULL, app_id uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('pause','resume')),
 expected_created_at timestamptz NOT NULL,
 sequence bigint NOT NULL CHECK(sequence>0),
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'),
 recorded_at timestamptz NOT NULL,
 retain_until timestamptz NOT NULL CHECK(retain_until>recorded_at),
 PRIMARY KEY(subscription_id,job_request_id,command_id),
 UNIQUE(subscription_id,job_request_id,sequence),
 FOREIGN KEY(subscription_id,job_request_id) REFERENCES event_execution_lineage_retry_jobs(subscription_id,request_id) ON DELETE CASCADE
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM event_execution_lineage_retry_control_receipts) THEN
  RAISE EXCEPTION 'prune retained lineage control receipts before downgrade';
 END IF;
END; $$;
-- +goose StatementEnd
DROP TABLE event_execution_lineage_retry_control_receipts;
