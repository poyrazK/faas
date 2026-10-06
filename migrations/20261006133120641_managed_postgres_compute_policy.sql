-- ADR-624: one fenced compute journal for class and idle-policy changes.
-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_resizes ADD COLUMN IF NOT EXISTS target_scale_to_zero boolean;
DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='managed_postgres_resizes'::regclass
  AND conname='managed_postgres_compute_policy_target_check') THEN
  ALTER TABLE managed_postgres_resizes ADD CONSTRAINT managed_postgres_compute_policy_target_check
   CHECK (target_scale_to_zero IS NULL OR (target_class IS NOT DISTINCT FROM source_spec->>'Class'
    AND jsonb_typeof(source_spec->'ScaleToZero') IS NOT DISTINCT FROM 'boolean'));
 END IF;
END $$;
CREATE OR REPLACE FUNCTION check_managed_postgres_resize_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r managed_postgres_resizes; d managed_postgres_databases; target_spec jsonb; actual_spec jsonb;
BEGIN
 SELECT * INTO r FROM managed_postgres_resizes WHERE id=NEW.id;
 IF NOT FOUND THEN RETURN NULL; END IF;
 SELECT * INTO d FROM managed_postgres_databases WHERE id=r.database_id FOR UPDATE;
 IF NOT FOUND THEN RETURN NULL; END IF;
 target_spec := r.source_spec || jsonb_build_object('Class',r.target_class);
 IF r.target_scale_to_zero IS NOT NULL THEN
  target_spec := target_spec || jsonb_build_object('ScaleToZero',r.target_scale_to_zero);
 END IF;
 actual_spec := jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,'Class',d.service_class,
  'Availability',d.availability,'ScaleToZero',d.scale_to_zero,'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds);
 IF r.account_id<>d.account_id OR (r.state='succeeded' AND d.observed_generation<r.generation)
  OR (r.state='succeeded' AND d.observed_generation=r.generation AND (target_spec IS DISTINCT FROM actual_spec
    OR r.backend_id<>d.backend_id OR r.backend_fingerprint<>d.backend_fingerprint
    OR r.provider_resource_id IS DISTINCT FROM d.provider_resource_id OR r.data_resource_id IS DISTINCT FROM d.data_resource_id))
  OR (r.state='pending' AND (d.state<>'updating' OR d.desired_generation<>r.generation OR d.observed_generation<>r.generation-1
    OR d.environment_clone_operation_id IS NOT NULL OR d.clone_resource_role<>'target' OR d.cutover_id IS NOT NULL
    OR r.backend_id<>d.backend_id OR r.backend_fingerprint<>d.backend_fingerprint
    OR r.provider_resource_id IS DISTINCT FROM d.provider_resource_id OR r.data_resource_id IS DISTINCT FROM d.data_resource_id
    OR r.source_spec<>jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,'Class',d.service_class,
      'Availability',d.availability,'ScaleToZero',d.scale_to_zero,'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds))) THEN
  RAISE EXCEPTION 'resize receipt does not match database' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
 RETURN NULL;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_resizes WHERE target_scale_to_zero IS NOT NULL) THEN
  RAISE EXCEPTION 'retain compute policy history before rollback' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION check_managed_postgres_resize_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r managed_postgres_resizes; d managed_postgres_databases;
BEGIN
 SELECT * INTO r FROM managed_postgres_resizes WHERE id=NEW.id;
 IF NOT FOUND THEN RETURN NULL; END IF;
 SELECT * INTO d FROM managed_postgres_databases WHERE id=r.database_id FOR UPDATE;
 IF NOT FOUND THEN RETURN NULL; END IF;
 IF r.account_id<>d.account_id OR (r.state='succeeded' AND d.observed_generation<r.generation)
  OR (r.state='pending' AND (d.state<>'updating' OR d.desired_generation<>r.generation OR d.observed_generation<>r.generation-1
    OR d.environment_clone_operation_id IS NOT NULL OR d.clone_resource_role<>'target' OR d.cutover_id IS NOT NULL
    OR r.backend_id<>d.backend_id OR r.backend_fingerprint<>d.backend_fingerprint
    OR r.provider_resource_id IS DISTINCT FROM d.provider_resource_id OR r.data_resource_id IS DISTINCT FROM d.data_resource_id
    OR r.source_spec<>jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,'Class',d.service_class,
      'Availability',d.availability,'ScaleToZero',d.scale_to_zero,'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds))) THEN
  RAISE EXCEPTION 'resize receipt does not match database' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
 RETURN NULL;
END;
$$;
ALTER TABLE managed_postgres_resizes DROP CONSTRAINT managed_postgres_compute_policy_target_check;
ALTER TABLE managed_postgres_resizes DROP COLUMN target_scale_to_zero;
-- +goose StatementEnd
