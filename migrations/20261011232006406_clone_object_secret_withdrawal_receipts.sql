-- filename: 20261011232006406_clone_object_secret_withdrawal_receipts.sql

-- +goose Up
-- ADR-590: original ownership survives atomic Object-storage envelope withdrawal.
CREATE TABLE IF NOT EXISTS project_environment_clone_object_secret_withdrawals (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_configuration_ownership(operation_id) ON DELETE RESTRICT,
 ownership_sha256 text NOT NULL CHECK(ownership_sha256 ~ '^[a-f0-9]{64}$'),
 postgres_withdrawal_sha256 text NOT NULL CHECK(postgres_withdrawal_sha256='' OR postgres_withdrawal_sha256 ~ '^[a-f0-9]{64}$'),
 prior_manifest_sha256 text NOT NULL CHECK(prior_manifest_sha256 ~ '^[a-f0-9]{64}$'),
 publication_plan_sha256 text NOT NULL CHECK(publication_plan_sha256 ~ '^[a-f0-9]{64}$'),
 publication_withdrawn_at timestamptz NOT NULL,
 manifest jsonb NOT NULL CHECK(jsonb_typeof(manifest)='object' AND octet_length(manifest::text)<=32768),
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[a-f0-9]{64}$'),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 CHECK(recorded_at>=publication_withdrawn_at)
);

-- +goose Down
LOCK TABLE project_environment_clone_object_secret_withdrawals IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_object_secret_withdrawals) THEN
  RAISE EXCEPTION 'Object-storage secret withdrawal evidence is still retained'
   USING ERRCODE='23514',CONSTRAINT='clone_object_secret_withdrawal_down_no_evidence';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_object_secret_withdrawals;
