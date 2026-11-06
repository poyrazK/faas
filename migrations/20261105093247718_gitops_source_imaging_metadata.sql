-- migration-compat: expand
-- +goose Up
SET LOCAL lock_timeout = '5s';

-- Source archives stay authoritative after imaged derives an OCI identity and
-- runtime facts. Borrowed artifacts and reviewed runtime/source inputs retain
-- their existing immutable comparisons in the candidate trigger.
-- +goose StatementBegin
CREATE FUNCTION environment_source_imaging_metadata_update_valid(before_row deployments, after_row deployments) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
 SELECT coalesce(
  jsonb_typeof(before_row.environment_workload_runtime->'source_archive')='object'
  AND NOT (before_row.environment_workload_runtime ? 'built_artifact')
  AND before_row.environment_workload_runtime->'source'->>'kind' IN ('source','dockerfile','function')
  AND before_row.kind IN ('github','tarball')
  AND before_row.status IN ('pending','building','imaging','snapshotting')
  AND after_row.status=before_row.status
  AND coalesce(before_row.rootfs_path,'')<>''
  AND after_row.rootfs_path=before_row.rootfs_path
  AND before_row.build_id::text=before_row.environment_workload_runtime->'source_archive'->>'build_id'
  AND after_row.image_digest ~ '^sha256:[a-f0-9]{64}$'
  AND (coalesce(before_row.image_digest,'')='' OR after_row.image_digest=before_row.image_digest)
  AND (after_row.inferred_profile IS NOT DISTINCT FROM before_row.inferred_profile OR (
   before_row.status IN ('pending','building','imaging')
   AND jsonb_typeof(after_row.inferred_profile)='object'
   AND (coalesce(before_row.inferred_profile,'{}'::jsonb) - ARRAY['port','image_healthcheck_required']
    || CASE WHEN before_row.inferred_profile ? 'version' THEN '{}'::jsonb ELSE '{"version":"v1"}'::jsonb END
    || CASE WHEN before_row.inferred_profile ? 'framework' THEN '{}'::jsonb ELSE '{"framework":"unknown"}'::jsonb END)
     = (after_row.inferred_profile - ARRAY['port','image_healthcheck_required'])
   AND (NOT coalesce(before_row.inferred_profile ? 'port',false) OR after_row.inferred_profile ? 'port')
   AND (NOT (after_row.inferred_profile ? 'port') OR (
    jsonb_typeof(after_row.inferred_profile->'port')='number'
    AND after_row.inferred_profile->>'port' ~ '^[0-9]{1,5}$'
    AND (after_row.inferred_profile->>'port')::numeric BETWEEN 1 AND 65535))
   AND (NOT coalesce(before_row.inferred_profile ? 'image_healthcheck_required',false) OR after_row.inferred_profile ? 'image_healthcheck_required')
   AND (NOT (after_row.inferred_profile ? 'image_healthcheck_required') OR jsonb_typeof(after_row.inferred_profile->'image_healthcheck_required')='boolean')
  )),false);
$$;
-- +goose StatementEnd

-- Change only the two derived-field comparisons; all provenance, deployment
-- inputs, activation and artifact guards continue to execute unchanged.
-- +goose StatementBegin
DO $imaging$
DECLARE definition text; original_profile text; derived_profile text; original_identity text; derived_identity text;
BEGIN
 original_profile:='NEW.source_url,NEW.log_path,NEW.inferred_profile,NEW.github_source_ref';
 derived_profile:='NEW.source_url,NEW.log_path,CASE WHEN environment_source_imaging_metadata_update_valid(OLD,NEW) THEN OLD.inferred_profile ELSE NEW.inferred_profile END,NEW.github_source_ref';
 original_identity:='ROW(NEW.app_id,NEW.scope,NEW.kind,NEW.image_digest,NEW.commit_sha';
 derived_identity:='ROW(NEW.app_id,NEW.scope,NEW.kind,CASE WHEN environment_source_imaging_metadata_update_valid(OLD,NEW) THEN OLD.image_digest ELSE NEW.image_digest END,NEW.commit_sha';
 definition:=pg_get_functiondef('guard_environment_workload_candidate()'::regprocedure);
 IF position(original_profile IN definition)=0 OR position(original_identity IN definition)=0 THEN
  RAISE EXCEPTION 'candidate immutability guard differs from the expected imaging migration contract';
 END IF;
 EXECUTE replace(replace(definition,original_profile,derived_profile),original_identity,derived_identity);
END;
$imaging$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
-- +goose StatementBegin
DO $imaging$
DECLARE definition text;
BEGIN
 definition:=pg_get_functiondef('guard_environment_workload_candidate()'::regprocedure);
 definition:=replace(definition,
  'CASE WHEN environment_source_imaging_metadata_update_valid(OLD,NEW) THEN OLD.inferred_profile ELSE NEW.inferred_profile END',
  'NEW.inferred_profile');
 definition:=replace(definition,
  'CASE WHEN environment_source_imaging_metadata_update_valid(OLD,NEW) THEN OLD.image_digest ELSE NEW.image_digest END',
  'NEW.image_digest');
 EXECUTE definition;
END;
$imaging$;
-- +goose StatementEnd
DROP FUNCTION environment_source_imaging_metadata_update_valid(deployments,deployments);
