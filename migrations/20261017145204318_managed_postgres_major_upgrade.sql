-- filename: 20261017145204318_managed_postgres_major_upgrade.sql
-- ADR-1027: the compute-change journal also carries in-place major upgrades.
-- An upgrade changes the major alone, to a later one. Unlike the other
-- changes, it can end without succeeding: the provider refused it, or it
-- broke the cluster. It then ends `failed` with a stable error_code, and the
-- database is back on its source spec at the change's generation. Only an
-- upgrade can fail; an outcome is final.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE managed_postgres_resizes
 ADD COLUMN IF NOT EXISTS target_postgres_major integer,
 ADD COLUMN IF NOT EXISTS error_code text;
ALTER TABLE managed_postgres_resizes
 DROP CONSTRAINT IF EXISTS managed_postgres_resizes_state_check,
 DROP CONSTRAINT IF EXISTS managed_postgres_resizes_check,
 DROP CONSTRAINT IF EXISTS managed_postgres_resize_outcome_check,
 DROP CONSTRAINT IF EXISTS managed_postgres_major_upgrade_target_check;
ALTER TABLE managed_postgres_resizes
 ADD CONSTRAINT managed_postgres_resizes_state_check CHECK (state IN ('pending','succeeded','failed')),
 ADD CONSTRAINT managed_postgres_resizes_check CHECK ((state='pending' AND completed_at IS NULL)
  OR (state IN ('succeeded','failed') AND completed_at IS NOT NULL AND completed_at>=created_at)),
 ADD CONSTRAINT managed_postgres_resize_outcome_check CHECK ((state='failed')=(error_code IS NOT NULL)
  AND (error_code IS NULL OR error_code ~ '^[a-z_]{1,64}$') AND (state<>'failed' OR target_postgres_major IS NOT NULL)),
 ADD CONSTRAINT managed_postgres_major_upgrade_target_check CHECK (target_postgres_major IS NULL OR (target_scale_to_zero IS NULL
  AND target_storage_limit_bytes IS NULL AND target_class=(source_spec->>'Class')
  AND jsonb_typeof(source_spec->'PostgresMajor')='number'
  AND target_postgres_major>(source_spec->>'PostgresMajor')::numeric AND target_postgres_major<=99));

CREATE OR REPLACE FUNCTION guard_managed_postgres_resize_intent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.state='pending' AND EXISTS(SELECT 1 FROM managed_postgres_databases WHERE id=OLD.database_id) THEN
   RAISE EXCEPTION 'pending resize intent cannot be removed' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
  END IF;
  RETURN OLD;
 END IF;
 IF (to_jsonb(NEW)-'state'-'completed_at'-'error_code') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'completed_at'-'error_code')
  OR (OLD.state<>'pending' AND NEW IS DISTINCT FROM OLD) THEN
  RAISE EXCEPTION 'resize intent is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
 RETURN NEW;
END;
$$;

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
 IF r.target_storage_limit_bytes IS NOT NULL THEN
  target_spec := target_spec || jsonb_build_object('StorageLimitBytes',r.target_storage_limit_bytes);
 END IF;
 IF r.target_postgres_major IS NOT NULL THEN
  target_spec := target_spec || jsonb_build_object('PostgresMajor',r.target_postgres_major);
 END IF;
 actual_spec := jsonb_build_object('Region',d.region,'PostgresMajor',d.postgres_major,'Class',d.service_class,
  'Availability',d.availability,'ScaleToZero',d.scale_to_zero,'StorageLimitBytes',d.storage_limit_bytes,'RestoreWindowSeconds',d.restore_window_seconds);
 IF r.account_id<>d.account_id OR (r.state IN ('succeeded','failed') AND d.observed_generation<r.generation)
  OR (r.state IN ('succeeded','failed') AND d.observed_generation=r.generation
   AND ((CASE WHEN r.state='succeeded' THEN target_spec ELSE r.source_spec END) IS DISTINCT FROM actual_spec
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
 IF EXISTS(SELECT 1 FROM managed_postgres_resizes WHERE target_postgres_major IS NOT NULL OR state='failed') THEN
  RAISE EXCEPTION 'retain major upgrade history before rollback' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
END $$;
CREATE OR REPLACE FUNCTION guard_managed_postgres_resize_intent() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  IF OLD.state='pending' AND EXISTS(SELECT 1 FROM managed_postgres_databases WHERE id=OLD.database_id) THEN
   RAISE EXCEPTION 'pending resize intent cannot be removed' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
  END IF;
  RETURN OLD;
 END IF;
 IF (to_jsonb(NEW)-'state'-'completed_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'completed_at')
  OR (OLD.state='succeeded' AND NEW IS DISTINCT FROM OLD) THEN
  RAISE EXCEPTION 'resize intent is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
 RETURN NEW;
END;
$$;
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
 IF r.target_storage_limit_bytes IS NOT NULL THEN
  target_spec := target_spec || jsonb_build_object('StorageLimitBytes',r.target_storage_limit_bytes);
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
ALTER TABLE managed_postgres_resizes
 DROP CONSTRAINT IF EXISTS managed_postgres_major_upgrade_target_check,
 DROP CONSTRAINT IF EXISTS managed_postgres_resize_outcome_check,
 DROP CONSTRAINT IF EXISTS managed_postgres_resizes_check,
 DROP CONSTRAINT IF EXISTS managed_postgres_resizes_state_check;
ALTER TABLE managed_postgres_resizes
 ADD CONSTRAINT managed_postgres_resizes_state_check CHECK (state IN ('pending','succeeded')),
 ADD CONSTRAINT managed_postgres_resizes_check CHECK ((state='pending' AND completed_at IS NULL)
  OR (state='succeeded' AND completed_at IS NOT NULL AND completed_at>=created_at));
ALTER TABLE managed_postgres_resizes
 DROP COLUMN IF EXISTS error_code,
 DROP COLUMN IF EXISTS target_postgres_major;
-- +goose StatementEnd
