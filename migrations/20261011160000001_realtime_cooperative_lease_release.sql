-- +goose Up
ALTER TABLE managed_realtime_consumer_group_messages ADD COLUMN IF NOT EXISTS release_count bigint NOT NULL DEFAULT 0 CHECK (release_count>=0);
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_permissions_check' AND position('''release''' in pg_get_constraintdef(oid))>0) OR NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_worker_credentials') AND conname='managed_realtime_worker_credentials_last_operation_check' AND position('''release''' in pg_get_constraintdef(oid))>0) THEN
ALTER TABLE managed_realtime_worker_credentials DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_permissions_check,DROP CONSTRAINT IF EXISTS managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK (cardinality(permissions) BETWEEN 1 AND 14 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain','heartbeat','release']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain','heartbeat','release'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
DO $realtime_replay$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('managed_realtime_group_delivery_history') AND conname='managed_realtime_group_delivery_history_event_check' AND position('''released''' in pg_get_constraintdef(oid))>0) THEN
ALTER TABLE managed_realtime_group_delivery_history DROP CONSTRAINT IF EXISTS managed_realtime_group_delivery_history_event_check;
ALTER TABLE managed_realtime_group_delivery_history ADD CONSTRAINT managed_realtime_group_delivery_history_event_check CHECK (event IN ('claimed','retried','lease_expired','dead_lettered','deadline_expired','invalid_deadline','completed','replayed','released'));
 END IF;
END $realtime_replay$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION measure_realtime_group_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wait_ms numeric := 0; first_count bigint := 0; retry_count bigint := 0; dead_count bigint := 0;
BEGIN
 IF NEW.status='leased' AND OLD.status<>'leased' THEN
  NEW.processing_started_at:=clock_timestamp();
  IF NOT OLD.first_claim_observed THEN
   NEW.first_claim_observed:=true;
   -- Pre-migration attempted rows have no first-claim timing baseline.
   IF OLD.attempts=0 THEN
    first_count:=1;
    wait_ms:=greatest(0,extract(epoch FROM (NEW.processing_started_at-(NEW.message->>'created_at')::timestamptz))*1000);
   END IF;
  END IF;
 END IF;
 IF OLD.status='leased' AND NEW.status='ready' AND NEW.release_count=OLD.release_count THEN retry_count:=1; END IF;
 IF OLD.status<>'dead' AND NEW.status='dead' THEN dead_count:=1; END IF;
 IF NEW.status<>'leased' THEN NEW.processing_started_at:=NULL; END IF;
 IF first_count+retry_count+dead_count>0 THEN
  PERFORM record_realtime_group_metric(NEW.group_id,first_count,0,retry_count,dead_count,wait_ms,0);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION observe_realtime_circuit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status='leased' AND NEW.status='ready' AND NEW.release_count>OLD.release_count THEN
  UPDATE managed_realtime_consumer_groups SET circuit_probe_sequence=NULL,circuit_probe_fencing_token=NULL
   WHERE id=NEW.group_id AND circuit_state='half_open' AND circuit_probe_sequence=OLD.sequence AND circuit_probe_fencing_token=OLD.fencing_token;
  RETURN NEW;
 END IF;
 IF OLD.status='leased' AND NEW.status IN ('ready','dead') THEN
  PERFORM observe_realtime_circuit(NEW.group_id,NEW.sequence,OLD.fencing_token,false,
   CASE WHEN NEW.failure_code='lease_expired' THEN 'lease_expired'
        WHEN NEW.deadline_invalid OR (NEW.delivery_deadline IS NOT NULL AND NEW.delivery_deadline<=clock_timestamp()) THEN 'deadline'
        ELSE 'handler_failure' END);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION observe_realtime_delivery_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE lease_expired boolean;
BEGIN
 IF OLD.status='leased' AND NEW.status='ready' AND NEW.release_count>OLD.release_count THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'released',OLD.attempts,OLD.fencing_token,'');
 ELSIF NEW.status='leased' AND OLD.status='ready' THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'claimed',NEW.attempts,NEW.fencing_token,'');
 ELSIF OLD.status='dead' AND NEW.status='ready' THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'replayed',NEW.attempts,NEW.fencing_token,'');
 ELSIF NEW.status='dead' AND OLD.status<>'dead' THEN
  IF NEW.deadline_invalid THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'invalid_deadline',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  ELSIF NEW.delivery_deadline IS NOT NULL AND NEW.delivery_deadline<=clock_timestamp() THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'deadline_expired',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  ELSIF OLD.status='leased' AND OLD.lease_until<=clock_timestamp() AND NEW.failure_code='lease_expired' THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'lease_expired',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  END IF;
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'dead_lettered',NEW.attempts,NEW.fencing_token,NEW.failure_code);
 ELSIF OLD.status='leased' AND NEW.status='ready' THEN
  lease_expired:=OLD.lease_until<=clock_timestamp() AND NEW.failure_code='lease_expired';
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,CASE WHEN lease_expired THEN 'lease_expired' ELSE 'retried' END,NEW.attempts,NEW.fencing_token,NEW.failure_code);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN IF EXISTS (SELECT 1 FROM managed_realtime_group_delivery_history WHERE event='released') THEN RAISE EXCEPTION 'release history must expire before downgrade'; END IF; END $$;
-- +goose StatementEnd
ALTER TABLE managed_realtime_worker_credentials DROP CONSTRAINT managed_realtime_worker_credentials_permissions_check,DROP CONSTRAINT managed_realtime_worker_credentials_last_operation_check;
ALTER TABLE managed_realtime_worker_credentials
 ADD CONSTRAINT managed_realtime_worker_credentials_permissions_check CHECK (cardinality(permissions) BETWEEN 1 AND 13 AND permissions <@ ARRAY['status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain','heartbeat']::text[]),
 ADD CONSTRAINT managed_realtime_worker_credentials_last_operation_check CHECK (last_operation IS NULL OR last_operation IN ('status','claim','renew','complete','retry','dead_letters','replay','resync','delete','pause','resume','drain','heartbeat'));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION measure_realtime_group_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE wait_ms numeric := 0; first_count bigint := 0; retry_count bigint := 0; dead_count bigint := 0;
BEGIN
 IF NEW.status='leased' AND OLD.status<>'leased' THEN
  NEW.processing_started_at:=clock_timestamp();
  IF NOT OLD.first_claim_observed THEN
   NEW.first_claim_observed:=true;
   -- Pre-migration attempted rows have no first-claim timing baseline.
   IF OLD.attempts=0 THEN
    first_count:=1;
    wait_ms:=greatest(0,extract(epoch FROM (NEW.processing_started_at-(NEW.message->>'created_at')::timestamptz))*1000);
   END IF;
  END IF;
 END IF;
 IF OLD.status='leased' AND NEW.status='ready' THEN retry_count:=1; END IF;
 IF OLD.status<>'dead' AND NEW.status='dead' THEN dead_count:=1; END IF;
 IF NEW.status<>'leased' THEN NEW.processing_started_at:=NULL; END IF;
 IF first_count+retry_count+dead_count>0 THEN
  PERFORM record_realtime_group_metric(NEW.group_id,first_count,0,retry_count,dead_count,wait_ms,0);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION observe_realtime_circuit_failure() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.status='leased' AND NEW.status IN ('ready','dead') THEN
  PERFORM observe_realtime_circuit(NEW.group_id,NEW.sequence,OLD.fencing_token,false,
   CASE WHEN NEW.failure_code='lease_expired' THEN 'lease_expired'
        WHEN NEW.deadline_invalid OR (NEW.delivery_deadline IS NOT NULL AND NEW.delivery_deadline<=clock_timestamp()) THEN 'deadline'
        ELSE 'handler_failure' END);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION observe_realtime_delivery_history() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE lease_expired boolean;
BEGIN
 IF NEW.status='leased' AND OLD.status='ready' THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'claimed',NEW.attempts,NEW.fencing_token,'');
 ELSIF OLD.status='dead' AND NEW.status='ready' THEN
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'replayed',NEW.attempts,NEW.fencing_token,'');
 ELSIF NEW.status='dead' AND OLD.status<>'dead' THEN
  IF NEW.deadline_invalid THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'invalid_deadline',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  ELSIF NEW.delivery_deadline IS NOT NULL AND NEW.delivery_deadline<=clock_timestamp() THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'deadline_expired',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  ELSIF OLD.status='leased' AND OLD.lease_until<=clock_timestamp() AND NEW.failure_code='lease_expired' THEN
   PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'lease_expired',NEW.attempts,NEW.fencing_token,NEW.failure_code);
  END IF;
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,'dead_lettered',NEW.attempts,NEW.fencing_token,NEW.failure_code);
 ELSIF OLD.status='leased' AND NEW.status='ready' THEN
  lease_expired:=OLD.lease_until<=clock_timestamp() AND NEW.failure_code='lease_expired';
  PERFORM record_realtime_delivery_history(NEW.group_id,NEW.sequence,CASE WHEN lease_expired THEN 'lease_expired' ELSE 'retried' END,NEW.attempts,NEW.fencing_token,NEW.failure_code);
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE managed_realtime_group_delivery_history DROP CONSTRAINT managed_realtime_group_delivery_history_event_check;
ALTER TABLE managed_realtime_group_delivery_history ADD CONSTRAINT managed_realtime_group_delivery_history_event_check CHECK (event IN ('claimed','retried','lease_expired','dead_lettered','deadline_expired','invalid_deadline','completed','replayed'));
ALTER TABLE managed_realtime_consumer_group_messages DROP COLUMN release_count;
