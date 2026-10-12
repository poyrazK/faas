-- filename: 20261011232006359_clone_checkpoint_object_publications.sql
-- +goose Up
-- ADR-590: atomic private gateway metadata publication, still hidden until ready.
CREATE TABLE IF NOT EXISTS project_environment_clone_checkpoint_object_publications (
 operation_id uuid PRIMARY KEY REFERENCES project_environment_clone_checkpoint_object_values(operation_id) ON DELETE RESTRICT,
 plan jsonb NOT NULL CHECK(jsonb_typeof(plan)='object' AND octet_length(plan::text)<=16777216),
 plan_sha256 text NOT NULL CHECK(plan_sha256 ~ '^[a-f0-9]{64}$'),
 state text NOT NULL DEFAULT 'published' CHECK(state='published')
);
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_publications IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_publications) THEN
  RAISE EXCEPTION 'checkpoint object publications are still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_object_publications_down_no_ownership';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE project_environment_clone_checkpoint_object_publications;
