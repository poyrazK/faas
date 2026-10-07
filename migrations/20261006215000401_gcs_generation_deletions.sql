-- +goose Up
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_provider_status_check;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_provider_status_check
 CHECK(provider_status IN ('','Enabled','Suspended','GCS_Enabled','GCS_Suspended'));
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_check7;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_check7
 CHECK(provider_status IN ('Enabled','GCS_Enabled','GCS_Suspended') OR baseline='[]');
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_gcs_generation_fence CHECK(
 provider_status NOT IN ('GCS_Enabled','GCS_Suspended') OR (
  selector='' AND target_provider_version_id='' AND reserved_bytes=0
  AND NOT delete_marker AND version_id='' AND provider_version_id=''
  AND ((state IN ('prepared','failed') AND baseline='[]') OR
   (jsonb_array_length(baseline)=1 AND jsonb_typeof(baseline->0)='string'
    AND (baseline->>0) ~ '^0{48}[0-7][0-9a-f]{15}$'))
 ));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM object_deletions WHERE provider_status IN ('GCS_Enabled','GCS_Suspended')) THEN
  RAISE EXCEPTION 'Cannot discard captured GCS generation fences';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_gcs_generation_fence;
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_check7;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_check7 CHECK(provider_status='Enabled' OR baseline='[]');
ALTER TABLE object_deletions DROP CONSTRAINT object_deletions_provider_status_check;
ALTER TABLE object_deletions ADD CONSTRAINT object_deletions_provider_status_check CHECK(provider_status IN ('','Enabled','Suspended'));
