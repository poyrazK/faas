-- +goose Up
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_pg_publications (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_checkpoint_pg_activations(operation_id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=25165824),
 plan_sha256 text NOT NULL CHECK(plan_sha256 ~ '^[a-f0-9]{64}$'),
 state text NOT NULL CHECK(state IN ('published','withdrawn')),
 published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 withdrawn_at timestamptz,
 CHECK((state='published' AND withdrawn_at IS NULL) OR (state='withdrawn' AND withdrawn_at IS NOT NULL))
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_pg_publications IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_pg_publications) THEN
  RAISE EXCEPTION 'checkpoint publication ownership is retained' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_pg_publications_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_pg_publications;
