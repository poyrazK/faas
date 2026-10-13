-- filename: 20261011232006371_clone_checkpoint_unused_object_reservations.sql

-- +goose Up
-- ADR-590: local completion evidence, never a native absence receipt.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_unused_object_reservations (
 operation_id uuid NOT NULL REFERENCES project_environment_clone_operations(id) ON DELETE RESTRICT,
 source_bucket_id uuid NOT NULL,
 target_bucket_id uuid NOT NULL REFERENCES object_buckets(id) ON DELETE RESTRICT,
 reservation jsonb NOT NULL CHECK(jsonb_typeof(reservation)='object'),
 completed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(operation_id,source_bucket_id),
 UNIQUE(target_bucket_id)
);

-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_unused_object_reservations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_unused_object_reservations) THEN
  RAISE EXCEPTION 'checkpoint unused reservation completion evidence is still owned'
   USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_unused_reservations_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_unused_object_reservations;
