-- A source-owned image may explicitly opt into full-rootfs dispatch. Keep
-- that deployment choice tied to the reviewed source, including when the
-- workload has no live deployment from which to inherit deployment inputs.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION guard_environment_workload_image_full_rootfs() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE reviewed_source jsonb;
BEGIN
 IF NEW.environment_workload_runtime IS NULL OR NEW.kind <> 'image' THEN
  RETURN NEW;
 END IF;
 SELECT w.source INTO reviewed_source
 FROM app_environment_workload_intents w
 JOIN project_environments e ON e.id=w.environment_id
 WHERE w.app_id=NEW.app_id AND e.slug=NEW.scope;
 IF reviewed_source IS NULL OR reviewed_source->>'kind' <> 'image' THEN
  RETURN NEW;
 END IF;
 IF NEW.environment_workload_runtime->'source' IS DISTINCT FROM reviewed_source OR
    coalesce(reviewed_source->>'full_rootfs','false') NOT IN ('true','false') THEN
  RAISE EXCEPTION 'frozen image source differs from reviewed workload intent' USING ERRCODE='23514';
 END IF;
 IF reviewed_source->>'full_rootfs'='true' THEN
  IF NEW.full_rootfs_override IS DISTINCT FROM true THEN
   RAISE EXCEPTION 'reviewed image requires full-rootfs opt-in' USING ERRCODE='23514';
  END IF;
 ELSIF NOT (NEW.environment_workload_runtime ? 'deployment_inputs') AND NEW.full_rootfs_override IS NOT NULL THEN
  RAISE EXCEPTION 'image full-rootfs override is not reviewed' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS guard_environment_workload_image_full_rootfs ON deployments;
CREATE TRIGGER guard_environment_workload_image_full_rootfs
BEFORE INSERT ON deployments FOR EACH ROW EXECUTE FUNCTION guard_environment_workload_image_full_rootfs();

-- +goose Down
DROP TRIGGER IF EXISTS guard_environment_workload_image_full_rootfs ON deployments;
DROP FUNCTION IF EXISTS guard_environment_workload_image_full_rootfs();
