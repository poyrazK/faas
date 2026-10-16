-- filename: 20261001231005610_application_standard_native_artifact_clock_fence.sql
-- adr: 393. Recheck the storage clock after all nonwaiting evidence reads.

-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION application_standard_lock_native_boot(instance_id uuid, expected_state text) RETURNS jsonb
    LANGUAGE plpgsql
    AS $$
DECLARE i instances%ROWTYPE; c instance_application_standard_admissions%ROWTYPE;
        input jsonb; incarnation uuid; artifact_deadline timestamptz; now_utc timestamptz;
BEGIN
 SELECT * INTO i FROM instances WHERE id=instance_id FOR UPDATE NOWAIT;
 IF NOT FOUND OR i.state IS DISTINCT FROM expected_state OR i.kind<>'wake' OR i.app_id IS NULL THEN
  RAISE EXCEPTION 'runtime boot state changed' USING ERRCODE='23514',CONSTRAINT='application_standard_boot_conflict';
 END IF;
 SELECT * INTO c FROM instance_application_standard_admissions WHERE instance_application_standard_admissions.instance_id=i.id FOR SHARE NOWAIT;
 input:=application_standard_native_runtime_snapshot(i.app_id,i.deployment_id) || jsonb_build_object('instance_ram_mb',i.ram_mb,'instance_mode',i.mode);
 IF c.instance_id IS NULL OR c.node_id IS DISTINCT FROM i.node_id OR c.input_snapshot IS DISTINCT FROM input
   OR (input->>'desired_revision')::bigint<=0 OR input->>'effective_hash'=''
   OR input->'desired_revision' IS DISTINCT FROM input->'persisted_revision'
   OR (input->'adoptions'='[]'::jsonb AND input->'materialized_fields'='[]'::jsonb) THEN
  RAISE EXCEPTION 'runtime admission inputs changed' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 SELECT vmmd_incarnation INTO incarnation FROM compute_nodes WHERE id=i.node_id FOR SHARE NOWAIT;
 IF incarnation IS NULL THEN
  RAISE EXCEPTION 'native process is not registered' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 now_utc:=clock_timestamp();
 IF expected_state<>'running' THEN
  artifact_deadline:=application_standard_native_artifact_deadline(input,now_utc);
 END IF;
 now_utc:=clock_timestamp();
 IF artifact_deadline IS NOT NULL AND artifact_deadline<=now_utc THEN
  RAISE EXCEPTION 'native artifact lease expired during its locked read' USING ERRCODE='23514',CONSTRAINT='application_standard_runtime_stale';
 END IF;
 RETURN jsonb_build_object('artifact_expires_at_unix_nano',(extract(epoch FROM artifact_deadline)*1000000000)::bigint,
  'input_snapshot',input,'captured_input_hash',c.native_input_hash,
  'node_id',i.node_id::text,'incarnation',incarnation::text,'clock_unix_nano',(extract(epoch FROM now_utc)*1000000000)::bigint);
EXCEPTION WHEN lock_not_available THEN
 RAISE EXCEPTION 'runtime inputs are busy' USING ERRCODE='55P03',CONSTRAINT='application_standard_runtime_busy';
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE WARNING 'native artifact storage clock fence is retained on Down'; END $$;
-- +goose StatementEnd
