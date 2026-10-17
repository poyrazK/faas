-- +goose Up
ALTER TABLE project_release_sets ADD COLUMN IF NOT EXISTS deactivation jsonb;

-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid='project_release_sets'::regclass AND conname='project_release_deactivation_receipt') THEN
  ALTER TABLE project_release_sets ADD CONSTRAINT project_release_deactivation_receipt CHECK (deactivation IS NULL OR
 (NOT active AND jsonb_typeof(deactivation)='object'
 AND jsonb_typeof(deactivation->'id')='string' AND (deactivation->>'id')::uuid IS NOT NULL
 AND deactivation->>'release_id'=id::text
 AND jsonb_typeof(deactivation->'completed_at')='string' AND (deactivation->>'completed_at')::timestamptz IS NOT NULL
 AND jsonb_typeof(deactivation->'fallback_members')='array' AND jsonb_array_length(deactivation->'fallback_members') BETWEEN 1 AND 100
 AND deactivation ?& ARRAY['id','release_id','completed_at','fallback_members']));
 END IF;
END $replay$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION authorize_binding_release_deactivation(target_project uuid,target_environment text,receipt_id uuid,prior_release text,members jsonb,fences jsonb) RETURNS boolean LANGUAGE plpgsql AS $$
DECLARE g jsonb;
BEGIN
 IF prior_release='' THEN RAISE EXCEPTION 'active graph required' USING ERRCODE='23514',CONSTRAINT='binding_release_changed'; END IF;
 IF EXISTS (SELECT 1 FROM deployments d JOIN apps a ON a.id=d.app_id WHERE a.project_id=target_project AND a.status<>'deleted' AND coalesce(a.preview_of_slug,'')='' AND d.scope=target_environment AND d.environment_workload_runtime IS NOT NULL) THEN
  RAISE EXCEPTION 'managed workload requires GitOps graph' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 PERFORM authorize_binding_release_graph(target_project,target_environment,receipt_id,prior_release,members,fences);
 g:=nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 -- This grant can retire the exact current graph, never insert a replacement.
 PERFORM set_config('gregale.binding_release_graph',(g || jsonb_build_object('new_id','','action','deactivate','receipt_id',receipt_id))::text,true);
 RETURN true;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_project_deactivation_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g jsonb;
BEGIN
 IF TG_OP='INSERT' THEN
  IF NEW.deactivation IS NOT NULL THEN RAISE EXCEPTION 'receipt requires retirement' USING ERRCODE='23514',CONSTRAINT='binding_release_required'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.deactivation IS NOT DISTINCT FROM OLD.deactivation THEN RETURN NEW; END IF;
 g:=nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF OLD.deactivation IS NOT NULL OR NEW.deactivation IS NULL OR g->>'action' IS DISTINCT FROM 'deactivate' OR g->>'old_id' IS DISTINCT FROM NEW.id::text OR NEW.deactivation->>'id' IS DISTINCT FROM g->>'receipt_id' THEN
  RAISE EXCEPTION 'immutable checked deactivation receipt' USING ERRCODE='23514',CONSTRAINT='binding_release_required';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='project_release_sets'::regclass AND tgname='project_deactivation_receipt_identity') THEN
  CREATE TRIGGER project_deactivation_receipt_identity BEFORE INSERT OR UPDATE ON project_release_sets FOR EACH ROW EXECUTE FUNCTION guard_project_deactivation_receipt();
 END IF;
END $replay$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION validate_checked_project_deactivation() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE g jsonb; receipt jsonb; selected jsonb; actual jsonb; a apps%ROWTYPE; expected text; f jsonb;
BEGIN
 g:=nullif(current_setting('gregale.binding_release_graph',true),'')::jsonb;
 IF g->>'action' IS DISTINCT FROM 'deactivate' THEN
  IF OLD.active AND NOT NEW.active AND g->>'old_id'=NEW.id::text AND NOT EXISTS (SELECT 1 FROM project_release_sets WHERE id=(g->>'new_id')::uuid AND active AND project_id=NEW.project_id AND environment_slug=NEW.environment_slug) THEN
   RAISE EXCEPTION 'forward grant requires replacement graph' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
  RETURN NEW;
 END IF;
 IF g->>'old_id' IS DISTINCT FROM NEW.id::text THEN RETURN NEW; END IF;
 SELECT deactivation INTO receipt FROM project_release_sets WHERE id=NEW.id AND NOT active;
 IF receipt IS NULL OR receipt->>'id' IS DISTINCT FROM g->>'receipt_id' OR EXISTS (SELECT 1 FROM project_release_sets WHERE project_id=NEW.project_id AND environment_slug=NEW.environment_slug AND active) THEN
  RAISE EXCEPTION 'deactivation lacks exact receipt' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 SELECT jsonb_object_agg(value->>'app_id',value->>'deployment_id') INTO selected FROM jsonb_array_elements(receipt->'fallback_members');
 IF selected IS DISTINCT FROM g->'members' OR jsonb_array_length(receipt->'fallback_members')<>(SELECT count(*) FROM jsonb_each(g->'members')) THEN
  RAISE EXCEPTION 'deactivation recipients changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 IF (SELECT count(*) FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' AND coalesce(preview_of_slug,'')='')<>(SELECT count(*) FROM jsonb_each(selected)) THEN
  RAISE EXCEPTION 'fallback membership changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
 END IF;
 FOR a IN SELECT * FROM apps WHERE project_id=NEW.project_id AND status<>'deleted' AND coalesce(preview_of_slug,'')='' ORDER BY id LOOP
  expected:=selected->>a.id::text;
  SELECT jsonb_object_agg(id::text,traffic_percent) INTO actual FROM deployments WHERE app_id=a.id AND scope=NEW.environment_slug AND status='live' AND traffic_percent>0;
  IF expected IS NULL OR actual IS DISTINCT FROM jsonb_build_object(expected,100) OR NOT EXISTS (SELECT 1 FROM deployments WHERE id=expected::uuid AND app_id=a.id AND scope=NEW.environment_slug AND status='live' AND NOT environment_workload_held AND coalesce(rootfs_key,'')<>'' AND coalesce(image_digest,'')<>'') THEN
   RAISE EXCEPTION 'fallback routing or eligibility changed' USING ERRCODE='23514',CONSTRAINT='binding_release_changed';
  END IF;
 END LOOP;
 FOR f IN SELECT value FROM jsonb_array_elements(g->'fences') LOOP
  IF nullif(f->>'valid_until','')::timestamptz<=clock_timestamp() THEN
   RAISE EXCEPTION 'deactivation evidence expired' USING ERRCODE='23514',CONSTRAINT='binding_release_expired';
  END IF;
 END LOOP;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $replay$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid='project_release_sets'::regclass AND tgname='checked_project_deactivation_commit') THEN
  CREATE CONSTRAINT TRIGGER checked_project_deactivation_commit AFTER UPDATE ON project_release_sets DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION validate_checked_project_deactivation();
 END IF;
END $replay$;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER checked_project_deactivation_commit ON project_release_sets;
DROP TRIGGER project_deactivation_receipt_identity ON project_release_sets;
DROP FUNCTION validate_checked_project_deactivation();
DROP FUNCTION guard_project_deactivation_receipt();
DROP FUNCTION authorize_binding_release_deactivation(uuid,text,uuid,text,jsonb,jsonb);
ALTER TABLE project_release_sets DROP CONSTRAINT project_release_deactivation_receipt,DROP COLUMN deactivation;
