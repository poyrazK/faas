-- filename: 20261013155647470_instances_frozen_state.sql

-- +goose Up
-- +goose StatementBegin
-- ADR-1151: an idle instance whose deployment has a usable snapshot can be
-- frozen in place (VM paused, resident, unbilled) instead of parked. FROZEN
-- holds RAM but no concurrency, vCPU or CPU, so it joins the resident sets
-- below but not the concurrency, CPU or ram_mb+8 node-usage sets. Billing is
-- unchanged: capture_instance_billing_interval lists its billable states, and
-- leaving FROZEN out closes an instance's interval while it is frozen.
ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_state_check;
ALTER TABLE instances
  ADD CONSTRAINT instances_state_check
  CHECK (state IN ('pending','parked','waking','cold_booting','running','draining','snapshotting','migrating','warm','stopped','failed','evicting_account_deleting','frozen')) NOT VALID;
ALTER TABLE instances VALIDATE CONSTRAINT instances_state_check;

ALTER TABLE managed_postgres_cutover_drain_plan_members
  DROP CONSTRAINT IF EXISTS managed_postgres_cutover_drain_plan_member_observed_state_check;
ALTER TABLE managed_postgres_cutover_drain_plan_members
  ADD CONSTRAINT managed_postgres_cutover_drain_plan_member_observed_state_check
  CHECK (observed_state IN ('pending','parked','waking','cold_booting','running','draining','snapshotting','migrating','warm','stopped','failed','evicting_account_deleting','frozen')) NOT VALID;
ALTER TABLE managed_postgres_cutover_drain_plan_members
  VALIDATE CONSTRAINT managed_postgres_cutover_drain_plan_member_observed_state_check;

-- The reaper lists frozen rows to park them after FrozenTTL.
DROP INDEX IF EXISTS instances_reaper_state_idx;
CREATE INDEX instances_reaper_state_idx ON instances (started_at DESC)
  WHERE state IN ('running', 'waking', 'cold_booting', 'snapshotting', 'warm', 'frozen', 'draining');

CREATE OR REPLACE FUNCTION capture_binding_promotion_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 old_row jsonb := CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 new_row jsonb := CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 old_facts jsonb;
 new_facts jsonb;
 row_data jsonb;
 target uuid;
 targets uuid[] := '{}';
 watched text[] := string_to_array(TG_ARGV[0],',');
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT jsonb_object_agg(key,value) INTO old_facts FROM jsonb_each(old_row) WHERE key=ANY(watched);
  SELECT jsonb_object_agg(key,value) INTO new_facts FROM jsonb_each(new_row) WHERE key=ANY(watched);
  IF old_facts IS NOT DISTINCT FROM new_facts THEN RETURN NEW; END IF;
 END IF;
 IF TG_TABLE_NAME='app_tasks' AND old_row->>'binding_verification' IS NULL AND new_row->>'binding_verification' IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 FOR row_data IN SELECT old_row UNION SELECT new_row LOOP
  IF row_data='{}'::jsonb THEN CONTINUE; END IF;
  CASE TG_TABLE_NAME
   WHEN 'apps' THEN targets:=targets||ARRAY[(row_data->>'id')::uuid];
   WHEN 'accounts' THEN
    targets:=targets||ARRAY(SELECT id FROM apps WHERE account_id=(row_data->>'id')::uuid);
   WHEN 'managed_postgres_databases' THEN
    targets:=targets||ARRAY(SELECT app_id FROM managed_postgres_bindings WHERE database_id=(row_data->>'id')::uuid);
   WHEN 'object_storage_s3_credentials' THEN
    targets:=targets||ARRAY[NULLIF(row_data->>'managed_app_id','')::uuid];
    targets:=targets||ARRAY(SELECT managed_app_id FROM object_storage_s3_credentials WHERE id=NULLIF(row_data->>'rotation_parent_id','')::uuid);
   WHEN 'outbound_integration_probe_policies' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'outbound_integrations' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'id')::uuid);
   WHEN 'outbound_integration_credentials' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'trigger_consumer_health' THEN
    targets:=targets||ARRAY(SELECT app_id FROM triggers WHERE id=(row_data->>'trigger_id')::uuid);
   WHEN 'notification_outbox' THEN
    IF row_data->>'channel'='runtime_config_restart' THEN
     BEGIN
      targets:=targets||ARRAY[NULLIF((row_data->>'payload')::jsonb->>'app_id','')::uuid];
     EXCEPTION WHEN invalid_text_representation THEN
      -- Malformed refresh metadata makes inventory unreadable; invalidate all
      -- approvals rather than allowing an old readable projection to survive.
      targets:=targets||ARRAY(SELECT id FROM apps);
     END;
    END IF;
   WHEN 'instances' THEN
    IF row_data->>'kind'='wake' AND row_data->>'mode'<>'mirror' AND
       row_data->>'state' IN ('running','waking','cold_booting','warm','frozen','snapshotting','draining','migrating') THEN
     targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
    END IF;
   ELSE targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
  END CASE;
 END LOOP;
 -- Stable ordering also serializes writes that affect multiple bound apps.
 FOR target IN SELECT DISTINCT id FROM unnest(targets) id WHERE id IS NOT NULL ORDER BY id LOOP
  PERFORM bump_binding_promotion_revision(target);
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION guard_worker_admission_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF (OLD.mode = 'worker' OR NEW.mode = 'worker') AND OLD.mode IS DISTINCT FROM NEW.mode THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      CONSTRAINT = 'instances_worker_admission_identity',
      MESSAGE = 'worker mode is immutable; create a reserved instance';
  END IF;
  IF NEW.mode = 'worker'
     AND OLD.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')
     AND NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen') THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      CONSTRAINT = 'instances_worker_admission_identity',
      MESSAGE = 'worker residency requires a newly reserved instance';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_instance_layer_artifacts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deployment_id IS NOT NULL AND NEW.state IN ('pending', 'waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'frozen', 'draining') THEN
        PERFORM require_deployment_layer_artifacts(NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- A frozen VM is disposable: its deployment has a snapshot. Rolled-back rows
-- park, as recovery after a restart would park them.
UPDATE instances SET state = 'parked' WHERE state = 'frozen';
UPDATE managed_postgres_cutover_drain_plan_members SET observed_state = 'parked' WHERE observed_state = 'frozen';

CREATE OR REPLACE FUNCTION capture_binding_promotion_revision() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 old_row jsonb := CASE WHEN TG_OP='INSERT' THEN '{}'::jsonb ELSE to_jsonb(OLD) END;
 new_row jsonb := CASE WHEN TG_OP='DELETE' THEN '{}'::jsonb ELSE to_jsonb(NEW) END;
 old_facts jsonb;
 new_facts jsonb;
 row_data jsonb;
 target uuid;
 targets uuid[] := '{}';
 watched text[] := string_to_array(TG_ARGV[0],',');
BEGIN
 IF TG_OP='UPDATE' THEN
  SELECT jsonb_object_agg(key,value) INTO old_facts FROM jsonb_each(old_row) WHERE key=ANY(watched);
  SELECT jsonb_object_agg(key,value) INTO new_facts FROM jsonb_each(new_row) WHERE key=ANY(watched);
  IF old_facts IS NOT DISTINCT FROM new_facts THEN RETURN NEW; END IF;
 END IF;
 IF TG_TABLE_NAME='app_tasks' AND old_row->>'binding_verification' IS NULL AND new_row->>'binding_verification' IS NULL THEN
  IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
 END IF;
 FOR row_data IN SELECT old_row UNION SELECT new_row LOOP
  IF row_data='{}'::jsonb THEN CONTINUE; END IF;
  CASE TG_TABLE_NAME
   WHEN 'apps' THEN targets:=targets||ARRAY[(row_data->>'id')::uuid];
   WHEN 'accounts' THEN
    targets:=targets||ARRAY(SELECT id FROM apps WHERE account_id=(row_data->>'id')::uuid);
   WHEN 'managed_postgres_databases' THEN
    targets:=targets||ARRAY(SELECT app_id FROM managed_postgres_bindings WHERE database_id=(row_data->>'id')::uuid);
   WHEN 'object_storage_s3_credentials' THEN
    targets:=targets||ARRAY[NULLIF(row_data->>'managed_app_id','')::uuid];
    targets:=targets||ARRAY(SELECT managed_app_id FROM object_storage_s3_credentials WHERE id=NULLIF(row_data->>'rotation_parent_id','')::uuid);
   WHEN 'outbound_integration_probe_policies' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'outbound_integrations' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'id')::uuid);
   WHEN 'outbound_integration_credentials' THEN
    targets:=targets||ARRAY(SELECT app_id FROM outbound_app_bindings WHERE integration_id=(row_data->>'integration_id')::uuid);
   WHEN 'trigger_consumer_health' THEN
    targets:=targets||ARRAY(SELECT app_id FROM triggers WHERE id=(row_data->>'trigger_id')::uuid);
   WHEN 'notification_outbox' THEN
    IF row_data->>'channel'='runtime_config_restart' THEN
     BEGIN
      targets:=targets||ARRAY[NULLIF((row_data->>'payload')::jsonb->>'app_id','')::uuid];
     EXCEPTION WHEN invalid_text_representation THEN
      -- Malformed refresh metadata makes inventory unreadable; invalidate all
      -- approvals rather than allowing an old readable projection to survive.
      targets:=targets||ARRAY(SELECT id FROM apps);
     END;
    END IF;
   WHEN 'instances' THEN
    IF row_data->>'kind'='wake' AND row_data->>'mode'<>'mirror' AND
       row_data->>'state' IN ('running','waking','cold_booting','warm','snapshotting','draining','migrating') THEN
     targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
    END IF;
   ELSE targets:=targets||ARRAY[(row_data->>'app_id')::uuid];
  END CASE;
 END LOOP;
 -- Stable ordering also serializes writes that affect multiple bound apps.
 FOR target IN SELECT DISTINCT id FROM unnest(targets) id WHERE id IS NOT NULL ORDER BY id LOOP
  PERFORM bump_binding_promotion_revision(target);
 END LOOP;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF; RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION guard_worker_admission_identity() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF (OLD.mode = 'worker' OR NEW.mode = 'worker') AND OLD.mode IS DISTINCT FROM NEW.mode THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      CONSTRAINT = 'instances_worker_admission_identity',
      MESSAGE = 'worker mode is immutable; create a reserved instance';
  END IF;
  IF NEW.mode = 'worker'
     AND OLD.state NOT IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
     AND NEW.state IN ('waking','cold_booting','running','draining','snapshotting','migrating','warm') THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      CONSTRAINT = 'instances_worker_admission_identity',
      MESSAGE = 'worker residency requires a newly reserved instance';
  END IF;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION guard_instance_layer_artifacts() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deployment_id IS NOT NULL AND NEW.state IN ('pending', 'waking', 'cold_booting', 'running', 'snapshotting', 'migrating', 'warm', 'draining') THEN
        PERFORM require_deployment_layer_artifacts(NEW.deployment_id);
    END IF;
    RETURN NEW;
END;
$$;

DROP INDEX IF EXISTS instances_reaper_state_idx;
CREATE INDEX instances_reaper_state_idx ON instances (started_at DESC)
  WHERE state IN ('running', 'waking', 'cold_booting', 'snapshotting', 'warm', 'draining');

ALTER TABLE managed_postgres_cutover_drain_plan_members
  DROP CONSTRAINT IF EXISTS managed_postgres_cutover_drain_plan_member_observed_state_check;
ALTER TABLE managed_postgres_cutover_drain_plan_members
  ADD CONSTRAINT managed_postgres_cutover_drain_plan_member_observed_state_check
  CHECK (observed_state IN ('pending','parked','waking','cold_booting','running','draining','snapshotting','migrating','warm','stopped','failed','evicting_account_deleting')) NOT VALID;
ALTER TABLE managed_postgres_cutover_drain_plan_members
  VALIDATE CONSTRAINT managed_postgres_cutover_drain_plan_member_observed_state_check;

ALTER TABLE instances DROP CONSTRAINT IF EXISTS instances_state_check;
ALTER TABLE instances
  ADD CONSTRAINT instances_state_check
  CHECK (state IN ('pending','parked','waking','cold_booting','running','draining','snapshotting','migrating','warm','stopped','failed','evicting_account_deleting')) NOT VALID;
ALTER TABLE instances VALIDATE CONSTRAINT instances_state_check;
-- +goose StatementEnd
