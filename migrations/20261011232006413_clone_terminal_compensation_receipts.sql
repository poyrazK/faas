-- +goose Up
-- ADR-590: private terminal compensation preserves original inventory and proof.
CREATE TABLE IF NOT EXISTS project_environment_clone_compensation_receipts (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_checkpoint_capture_retirements(operation_id) ON DELETE RESTRICT
  REFERENCES project_environment_clone_environment_cleanups(operation_id) ON DELETE RESTRICT
  REFERENCES project_environment_clone_deployment_cleanups(operation_id) ON DELETE RESTRICT,
 worker_token_sha256 text NOT NULL CHECK(worker_token_sha256 ~ '^[a-f0-9]{64}$'),
 report jsonb NOT NULL CHECK(jsonb_typeof(report)='object' AND octet_length(report::text)<=4096),
 receipt_sha256 text NOT NULL CHECK(receipt_sha256 ~ '^[a-f0-9]{64}$'),
 completed_at timestamptz NOT NULL
);

-- +goose Down
LOCK TABLE project_environment_clone_compensation_receipts IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_compensation_receipts) THEN
  RAISE EXCEPTION 'terminal compensation evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_compensation_receipt_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_compensation_receipts;
