-- filename: 20261023084254597_application_standard_snapshot_producer.sql
-- migration-compat: expand
-- ADR-595 extension (docs/adr/20261018-runtime-admission-rebind.md), open
-- question 3: a capture records the vmmd process that produced it. After a
-- re-bind a guest's receipt names its issuer while a successor captures it,
-- so the parent's incarnation no longer identifies the producer, and the
-- re-binds that would resolve it are deleted with the instance while the
-- catalog outlives it. The acknowledgment's lock has just checked the
-- source's effective incarnation against the node's current one; the guard
-- stamps that incarnation once and keeps it immutable. Rows acknowledged
-- before this migration keep NULL and are read as their parent's.

-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE application_standard_snapshot_captures ADD COLUMN IF NOT EXISTS producer_incarnation uuid;
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'application_standard_snapshot_captures'::regclass AND conname = 'application_standard_snapshot_captures_producer_check') THEN
  ALTER TABLE application_standard_snapshot_captures ADD CONSTRAINT application_standard_snapshot_captures_producer_check
   CHECK (producer_incarnation IS NULL OR acknowledgment IS NOT NULL AND producer_incarnation <> '00000000-0000-0000-0000-000000000000'::uuid) NOT VALID;
 END IF;
END
$$;
CREATE OR REPLACE FUNCTION application_standard_snapshot_capture_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE locked jsonb; parent jsonb; b jsonb; now_nano bigint; deadline bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) OR NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id)
   OR NOT EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'snapshot lineage is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF (to_jsonb(NEW)-ARRAY['acknowledgment','received_at','producer_incarnation']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['acknowledgment','received_at','producer_incarnation'])
   OR OLD.acknowledgment IS NOT NULL OR NEW.acknowledgment IS NULL OR OLD.producer_incarnation IS NOT NULL THEN
   RAISE EXCEPTION 'snapshot lineage is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.acknowledgment IS NOT NULL OR NEW.received_at IS NOT NULL THEN
  RAISE EXCEPTION 'snapshot acknowledgment requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_snapshot_capture(NEW.instance_id,NEW.expected_state);
 parent:=locked->'parent'; b:=parent->'binding'; now_nano:=(locked->>'clock_unix_nano')::bigint;
 deadline:=(locked->>'artifact_expires_at_unix_nano')::bigint;
 IF NEW.app_id::text IS DISTINCT FROM b->>'app_id' OR NEW.deployment_id::text IS DISTINCT FROM b->>'deployment_id'
  OR NEW.account_id::text IS DISTINCT FROM b->>'account_id' OR NEW.node_id::text IS DISTINCT FROM b->>'node_id'
  OR NEW.parent_token::text IS DISTINCT FROM b->>'token' OR NEW.memory_key IS DISTINCT FROM NEW.grant_data->>'memory_key'
  OR NOT application_standard_native_inputs_match(NEW.input_snapshot,locked->'input_snapshot')
  OR NOT application_standard_snapshot_grant_valid(NEW.grant_data,NEW.token,parent,NEW.expected_state,
   (locked->>'source_started_at_unix_nano')::bigint,now_nano,deadline) THEN
  RAISE EXCEPTION 'snapshot grant or scope is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='INSERT' THEN
  IF (NEW.grant_data->>'issued_at_unix_nano')::numeric<now_nano::numeric-5000000000
   OR NEW.input_snapshot::text IS DISTINCT FROM (locked->'input_snapshot')::text THEN
   RAISE EXCEPTION 'snapshot grant issue inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  NEW.created_at:=clock_timestamp(); NEW.producer_incarnation:=NULL;
 ELSE
  IF NOT application_standard_snapshot_acknowledgment_valid(NEW.acknowledgment,NEW.grant_data,now_nano) THEN
   RAISE EXCEPTION 'snapshot acknowledgment is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  NEW.received_at:=clock_timestamp();
  -- The lock just required the source's effective incarnation to be the
  -- node's current one: that process produced this capture.
  NEW.producer_incarnation:=(locked->>'incarnation')::uuid;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION application_standard_snapshot_capture_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE locked jsonb; parent jsonb; b jsonb; now_nano bigint; deadline bigint;
BEGIN
 IF TG_OP='DELETE' THEN
  IF NOT EXISTS(SELECT 1 FROM apps WHERE id=OLD.app_id) OR NOT EXISTS(SELECT 1 FROM deployments WHERE id=OLD.deployment_id)
   OR NOT EXISTS(SELECT 1 FROM accounts WHERE id=OLD.account_id) THEN RETURN OLD; END IF;
  RAISE EXCEPTION 'snapshot lineage is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF NEW IS NOT DISTINCT FROM OLD THEN RETURN NEW; END IF;
  IF (to_jsonb(NEW)-ARRAY['acknowledgment','received_at']) IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['acknowledgment','received_at'])
   OR OLD.acknowledgment IS NOT NULL OR NEW.acknowledgment IS NULL THEN
   RAISE EXCEPTION 'snapshot lineage is immutable' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
  END IF;
 ELSIF NEW.acknowledgment IS NOT NULL OR NEW.received_at IS NOT NULL THEN
  RAISE EXCEPTION 'snapshot acknowledgment requires a saved grant' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_immutable';
 END IF;
 locked:=application_standard_lock_snapshot_capture(NEW.instance_id,NEW.expected_state);
 parent:=locked->'parent'; b:=parent->'binding'; now_nano:=(locked->>'clock_unix_nano')::bigint;
 deadline:=(locked->>'artifact_expires_at_unix_nano')::bigint;
 IF NEW.app_id::text IS DISTINCT FROM b->>'app_id' OR NEW.deployment_id::text IS DISTINCT FROM b->>'deployment_id'
  OR NEW.account_id::text IS DISTINCT FROM b->>'account_id' OR NEW.node_id::text IS DISTINCT FROM b->>'node_id'
  OR NEW.parent_token::text IS DISTINCT FROM b->>'token' OR NEW.memory_key IS DISTINCT FROM NEW.grant_data->>'memory_key'
  OR NOT application_standard_native_inputs_match(NEW.input_snapshot,locked->'input_snapshot')
  OR NOT application_standard_snapshot_grant_valid(NEW.grant_data,NEW.token,parent,NEW.expected_state,
   (locked->>'source_started_at_unix_nano')::bigint,now_nano,deadline) THEN
  RAISE EXCEPTION 'snapshot grant or scope is stale' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 IF TG_OP='INSERT' THEN
  IF (NEW.grant_data->>'issued_at_unix_nano')::numeric<now_nano::numeric-5000000000
   OR NEW.input_snapshot::text IS DISTINCT FROM (locked->'input_snapshot')::text THEN
   RAISE EXCEPTION 'snapshot grant issue inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  NEW.created_at:=clock_timestamp();
 ELSE
  IF NOT application_standard_snapshot_acknowledgment_valid(NEW.acknowledgment,NEW.grant_data,now_nano) THEN
   RAISE EXCEPTION 'snapshot acknowledgment is invalid' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
  END IF;
  NEW.received_at:=clock_timestamp();
 END IF;
 RETURN NEW;
END;
$$;
ALTER TABLE application_standard_snapshot_captures DROP CONSTRAINT application_standard_snapshot_captures_producer_check,
 DROP COLUMN producer_incarnation;
-- +goose StatementEnd
