-- filename: 20260930193004001_exclusive_capture_barrier_recovery.sql
-- +goose Up
-- A replacement runtime may reuse an instance row after restore. Do not let a
-- crashed snapshot coordinator leave that row permanently unavailable. The
-- barrier remains set throughout a warm capture while state stays RUNNING;
-- a state/wake/node transition proves this runtime incarnation has changed.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION exclusive_work_instance_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_incarnation text;
BEGIN
  old_incarnation := old.id::text || '/' || old.wake_id::text || '/' || old.node_id::text;
  IF new.state IN ('snapshotting','parked') AND new.state IS DISTINCT FROM old.state
     AND EXISTS(SELECT 1 FROM exclusive_work_operations WHERE state='running'
       AND incarnation_id=old_incarnation AND lease_expires_at>clock_timestamp()
       AND attempt_deadline>clock_timestamp()) THEN
    RAISE EXCEPTION 'active exclusive operation prevents parking' USING errcode='55000';
  END IF;
  IF new.wake_id IS DISTINCT FROM old.wake_id OR new.node_id IS DISTINCT FROM old.node_id
     OR new.state IS DISTINCT FROM old.state THEN
    UPDATE exclusive_work_operations SET state='pending',claim_token=NULL,
      lease_expires_at=NULL,attempt_deadline=NULL,last_error='runtime incarnation revoked',
      due_at=clock_timestamp()
    WHERE state='running' AND incarnation_id=old_incarnation;
    NEW.exclusive_capture_blocked := false;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- Restore the prior runtime revocation behavior, which still fails closed for
-- fencing and rejects parking while an unexpired owner exists.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION exclusive_work_instance_transition() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE old_incarnation text;
BEGIN
  old_incarnation := old.id::text || '/' || old.wake_id::text || '/' || old.node_id::text;
  IF new.state IN ('snapshotting','parked') AND new.state IS DISTINCT FROM old.state
     AND EXISTS(SELECT 1 FROM exclusive_work_operations WHERE state='running'
       AND incarnation_id=old_incarnation AND lease_expires_at>clock_timestamp()
       AND attempt_deadline>clock_timestamp()) THEN
    RAISE EXCEPTION 'active exclusive operation prevents parking' USING errcode='55000';
  END IF;
  IF new.wake_id IS DISTINCT FROM old.wake_id OR new.node_id IS DISTINCT FROM old.node_id
     OR (new.state IS DISTINCT FROM old.state AND new.state<>'running') THEN
    UPDATE exclusive_work_operations SET state='pending',claim_token=NULL,
      lease_expires_at=NULL,attempt_deadline=NULL,last_error='runtime incarnation revoked'
    WHERE state='running' AND incarnation_id=old_incarnation;
  END IF;
  RETURN NEW;
END;
$$;
-- +goose StatementEnd
