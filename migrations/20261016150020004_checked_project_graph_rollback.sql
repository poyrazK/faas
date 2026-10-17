-- +goose Up
ALTER TABLE project_release_sets
 ADD COLUMN IF NOT EXISTS rollback_source_release_id uuid REFERENCES project_release_sets(id),
 ADD COLUMN IF NOT EXISTS rollback_previous_release_id uuid REFERENCES project_release_sets(id);

-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_sets'::regclass AND conname='project_release_rollback_pair') THEN
  ALTER TABLE project_release_sets ADD CONSTRAINT project_release_rollback_pair CHECK (
  (rollback_source_release_id IS NULL AND rollback_previous_release_id IS NULL) OR
  (rollback_source_release_id IS NOT NULL AND rollback_previous_release_id IS NOT NULL
   AND rollback_source_release_id <> rollback_previous_release_id
   AND rollback_source_release_id <> id AND rollback_previous_release_id <> id));
 END IF;
END $replay$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_checked_project_rollback() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE source project_release_sets%ROWTYPE; prior project_release_sets%ROWTYPE; grant_value jsonb; selected jsonb; historical jsonb;
BEGIN
 IF TG_OP='UPDATE' THEN
  IF NEW.rollback_source_release_id IS DISTINCT FROM OLD.rollback_source_release_id OR NEW.rollback_previous_release_id IS DISTINCT FROM OLD.rollback_previous_release_id THEN
   RAISE EXCEPTION 'immutable recovery identity' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  RETURN NEW;
 END IF;
 IF NEW.rollback_source_release_id IS NULL THEN RETURN NEW; END IF;
 grant_value := nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF grant_value IS NULL OR grant_value->>'new_id' IS DISTINCT FROM NEW.id::text OR grant_value->>'old_id' IS DISTINCT FROM NEW.rollback_previous_release_id::text THEN
  RAISE EXCEPTION 'rollback requires checked graph authorization' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
 END IF;
 SELECT * INTO source FROM project_release_sets WHERE id=NEW.rollback_source_release_id FOR SHARE;
 SELECT * INTO prior FROM project_release_sets WHERE id=NEW.rollback_previous_release_id FOR SHARE;
 IF source.account_id IS DISTINCT FROM NEW.account_id OR source.project_id IS DISTINCT FROM NEW.project_id OR source.environment_slug IS DISTINCT FROM NEW.environment_slug OR source.active OR source.expires_at IS NULL OR source.expires_at <= clock_timestamp()
  OR prior.account_id IS DISTINCT FROM NEW.account_id OR prior.project_id IS DISTINCT FROM NEW.project_id OR prior.environment_slug IS DISTINCT FROM NEW.environment_slug OR prior.active THEN
  RAISE EXCEPTION 'rollback history expired or changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 SELECT jsonb_object_agg(app_id::text,deployment_id::text) INTO selected FROM project_release_members WHERE release_id=NEW.id;
 SELECT jsonb_object_agg(app_id::text,deployment_id::text) INTO historical FROM project_release_members WHERE release_id=source.id;
 IF selected IS DISTINCT FROM historical THEN
  RAISE EXCEPTION 'rollback membership changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='project_release_sets'::regclass AND tgname='checked_project_rollback_commit') THEN
  CREATE CONSTRAINT TRIGGER checked_project_rollback_commit AFTER INSERT ON project_release_sets DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_checked_project_rollback();
 END IF;
END $replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='project_release_sets'::regclass AND tgname='checked_project_rollback_identity') THEN
  CREATE TRIGGER checked_project_rollback_identity BEFORE UPDATE ON project_release_sets FOR EACH ROW EXECUTE FUNCTION validate_checked_project_rollback();
 END IF;
END $replay$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER checked_project_rollback_identity ON project_release_sets;
DROP TRIGGER checked_project_rollback_commit ON project_release_sets;
DROP FUNCTION validate_checked_project_rollback();
ALTER TABLE project_release_sets DROP CONSTRAINT project_release_rollback_pair, DROP COLUMN rollback_source_release_id, DROP COLUMN rollback_previous_release_id;
