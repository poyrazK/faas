-- ADR-623: preserve an immutable, generation-bound compute resize intent.
-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS managed_postgres_resizes (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 database_id uuid NOT NULL REFERENCES managed_postgres_databases(id) ON DELETE CASCADE,
 backend_id text NOT NULL CHECK (length(backend_id)>0),
 backend_fingerprint text NOT NULL CHECK (backend_fingerprint ~ '^[a-f0-9]{64}$'),
 provider_resource_id text NOT NULL CHECK (length(provider_resource_id) BETWEEN 1 AND 255),
 data_resource_id text NOT NULL CHECK (length(data_resource_id) BETWEEN 1 AND 255),
 source_spec jsonb NOT NULL CHECK (jsonb_typeof(source_spec)='object'),
 target_class text NOT NULL CHECK (target_class IN ('development','burstable','production')),
 generation bigint NOT NULL CHECK (generation>1),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','succeeded')),
 created_at timestamptz NOT NULL,
 completed_at timestamptz,
 UNIQUE(database_id,generation),
 CHECK ((state='pending' AND completed_at IS NULL) OR (state='succeeded' AND completed_at IS NOT NULL AND completed_at>=created_at))
);
CREATE UNIQUE INDEX IF NOT EXISTS managed_postgres_resizes_active_idx ON managed_postgres_resizes(database_id) WHERE state='pending';

CREATE OR REPLACE FUNCTION guard_managed_postgres_resize_database() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_resizes WHERE database_id=OLD.id AND state='pending') THEN
  IF TG_OP='DELETE' THEN
   RAISE EXCEPTION 'database has an unresolved resize' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
  END IF;
  IF ROW(NEW.account_id,NEW.backend_id,NEW.backend_fingerprint,NEW.provider_resource_id,NEW.data_resource_id,
      NEW.region,NEW.postgres_major,NEW.service_class,NEW.availability,NEW.scale_to_zero,NEW.storage_limit_bytes,NEW.restore_window_seconds,
      NEW.desired_generation,NEW.observed_generation,NEW.cutover_id,NEW.environment_clone_operation_id,NEW.clone_resource_role)
      IS DISTINCT FROM ROW(OLD.account_id,OLD.backend_id,OLD.backend_fingerprint,OLD.provider_resource_id,OLD.data_resource_id,
      OLD.region,OLD.postgres_major,OLD.service_class,OLD.availability,OLD.scale_to_zero,OLD.storage_limit_bytes,OLD.restore_window_seconds,
      OLD.desired_generation,OLD.observed_generation,OLD.cutover_id,OLD.environment_clone_operation_id,OLD.clone_resource_role)
      OR NEW.state<>'updating' THEN
   RAISE EXCEPTION 'database has an unresolved resize' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_resize_database_guard ON managed_postgres_databases;
CREATE TRIGGER managed_postgres_resize_database_guard BEFORE UPDATE OR DELETE ON managed_postgres_databases
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_resize_database();

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
DROP TRIGGER IF EXISTS managed_postgres_resize_intent_guard ON managed_postgres_resizes;
CREATE TRIGGER managed_postgres_resize_intent_guard BEFORE UPDATE OR DELETE ON managed_postgres_resizes
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_resize_intent();

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
DROP TRIGGER IF EXISTS managed_postgres_resize_receipt_guard ON managed_postgres_resizes;
CREATE CONSTRAINT TRIGGER managed_postgres_resize_receipt_guard AFTER INSERT OR UPDATE ON managed_postgres_resizes
 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION check_managed_postgres_resize_receipt();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM managed_postgres_resizes WHERE state='pending') THEN
  RAISE EXCEPTION 'finish pending resizes before rollback' USING ERRCODE='23514', CONSTRAINT='managed_postgres_resize_conflict';
 END IF;
END $$;
DROP TRIGGER IF EXISTS managed_postgres_resize_database_guard ON managed_postgres_databases;
DROP FUNCTION IF EXISTS guard_managed_postgres_resize_database();
DROP TABLE managed_postgres_resizes;
DROP FUNCTION IF EXISTS guard_managed_postgres_resize_intent();
DROP FUNCTION IF EXISTS check_managed_postgres_resize_receipt();
-- +goose StatementEnd
