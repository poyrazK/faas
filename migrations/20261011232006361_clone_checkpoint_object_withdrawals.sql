-- filename: 20261011232006361_clone_checkpoint_object_withdrawals.sql
-- +goose Up
-- ADR-590: permanently retire owned private gateway metadata during compensation.
ALTER TABLE project_environment_clone_checkpoint_object_publications
 DROP CONSTRAINT project_environment_clone_checkpoint_object_publica_state_check,
 ADD COLUMN withdrawn_at timestamptz,
 ADD CONSTRAINT clone_checkpoint_object_publications_state_shape CHECK (
  (state='published' AND withdrawn_at IS NULL) OR
  (state='withdrawn' AND withdrawn_at IS NOT NULL)
 );
-- +goose Down
LOCK TABLE project_environment_clone_checkpoint_object_publications IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM project_environment_clone_checkpoint_object_publications WHERE state='withdrawn') THEN
  RAISE EXCEPTION 'checkpoint object retirement is still owned' USING ERRCODE='23514',CONSTRAINT='clone_checkpoint_object_withdrawals_down_no_retirement';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE project_environment_clone_checkpoint_object_publications
 DROP CONSTRAINT clone_checkpoint_object_publications_state_shape,
 DROP COLUMN withdrawn_at,
 ADD CONSTRAINT project_environment_clone_checkpoint_object_publica_state_check CHECK(state='published');
