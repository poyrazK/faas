-- filename: 20261001175350459_managed_postgres_cutover_admission_fence.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-466: app-wide admission barrier, separate from writer-drain evidence.
ALTER TABLE apps ADD COLUMN IF NOT EXISTS managed_postgres_admission_cutover_id uuid REFERENCES managed_postgres_cutovers(id) ON DELETE RESTRICT;
ALTER TABLE apps ADD COLUMN IF NOT EXISTS managed_postgres_admission_fenced_at timestamptz;
ALTER TABLE apps DROP CONSTRAINT IF EXISTS apps_managed_postgres_admission_fence_check;
ALTER TABLE apps ADD CONSTRAINT apps_managed_postgres_admission_fence_check
 CHECK ((managed_postgres_admission_cutover_id IS NULL)=(managed_postgres_admission_fenced_at IS NULL));

CREATE OR REPLACE FUNCTION guard_managed_postgres_admission_fence() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE intent managed_postgres_cutovers%ROWTYPE; checked_at timestamptz;
BEGIN
 IF NEW.managed_postgres_admission_cutover_id IS NOT DISTINCT FROM OLD.managed_postgres_admission_cutover_id THEN
  IF NEW.managed_postgres_admission_fenced_at IS DISTINCT FROM OLD.managed_postgres_admission_fenced_at
   OR (OLD.managed_postgres_admission_cutover_id IS NOT NULL AND ROW(NEW.id,NEW.account_id) IS DISTINCT FROM ROW(OLD.id,OLD.account_id)) THEN
   RAISE EXCEPTION 'admission fence is immutable' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
  RETURN NEW;
 END IF;
 IF OLD.managed_postgres_admission_cutover_id IS NOT NULL THEN
  SELECT * INTO intent FROM managed_postgres_cutovers WHERE id=OLD.managed_postgres_admission_cutover_id FOR SHARE;
  IF NEW.managed_postgres_admission_cutover_id IS NOT NULL OR intent.state IS DISTINCT FROM 'cancelled' THEN
   RAISE EXCEPTION 'admission fence cannot be released' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
 ELSE
  SELECT * INTO intent FROM managed_postgres_cutovers WHERE id=NEW.managed_postgres_admission_cutover_id FOR SHARE;
  checked_at := clock_timestamp();
  IF intent.app_id IS DISTINCT FROM NEW.id OR intent.account_id IS DISTINCT FROM NEW.account_id
   OR intent.state IS DISTINCT FROM 'verified' OR intent.lease_token IS NOT NULL
   OR intent.verified_at IS NULL OR intent.verified_at>checked_at OR intent.verified_at<checked_at-interval '5 minutes'
   OR NEW.status NOT IN ('active','evicted_cold')
   OR NOT EXISTS (SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=intent.id)
   OR EXISTS (SELECT 1 FROM managed_postgres_cutover_credentials WHERE cutover_id=intent.id
    AND (state<>'sealed' OR verified_at IS NULL OR verified_at>checked_at OR verified_at<checked_at-interval '5 minutes')) THEN
   RAISE EXCEPTION 'cutover verification is not fresh' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
  END IF;
  NEW.managed_postgres_admission_fenced_at := checked_at;
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_admission_fence_guard ON apps;
CREATE TRIGGER managed_postgres_admission_fence_guard BEFORE INSERT OR UPDATE ON apps
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_admission_fence();

CREATE OR REPLACE FUNCTION guard_managed_postgres_instance_admission() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE pinned uuid;
BEGIN
 IF NEW.app_id IS NULL THEN RETURN NEW; END IF;
 IF TG_OP='INSERT' THEN
  IF NEW.state IN ('parked','stopped','failed') THEN RETURN NEW; END IF;
 ELSIF ROW(NEW.app_id,NEW.state) IS NOT DISTINCT FROM ROW(OLD.app_id,OLD.state)
  OR NEW.state NOT IN ('waking','cold_booting','running','warm') THEN
  RETURN NEW;
 END IF;
 -- A SHARE lock conflicts with the app UPDATE that installs the fence.
 -- The updated app tuple also fences transactions using repeatable-read.
 SELECT managed_postgres_admission_cutover_id INTO pinned FROM apps WHERE id=NEW.app_id FOR SHARE;
 IF pinned IS NOT NULL THEN
  RAISE EXCEPTION 'instance admission is fenced by a database cutover' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_admission_fenced';
 END IF;
 RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS managed_postgres_instance_admission_guard ON instances;
CREATE TRIGGER managed_postgres_instance_admission_guard BEFORE INSERT OR UPDATE OF app_id,state ON instances
 FOR EACH ROW EXECUTE FUNCTION guard_managed_postgres_instance_admission();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM apps WHERE managed_postgres_admission_cutover_id IS NOT NULL) THEN
  RAISE EXCEPTION 'cancel fenced cutovers before rollback' USING ERRCODE='23514', CONSTRAINT='managed_postgres_cutover_conflict';
 END IF;
END $$;
DROP TRIGGER managed_postgres_instance_admission_guard ON instances;
DROP FUNCTION guard_managed_postgres_instance_admission();
DROP TRIGGER managed_postgres_admission_fence_guard ON apps;
DROP FUNCTION guard_managed_postgres_admission_fence();
ALTER TABLE apps DROP CONSTRAINT apps_managed_postgres_admission_fence_check;
ALTER TABLE apps DROP COLUMN managed_postgres_admission_fenced_at;
ALTER TABLE apps DROP COLUMN managed_postgres_admission_cutover_id;
-- +goose StatementEnd
