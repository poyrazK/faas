-- filename: 20260930175302003_exclusive_work_runtime_fence.sql

-- +goose Up
-- +goose StatementBegin
create or replace function exclusive_work_instance_transition() returns trigger language plpgsql as $$
declare old_incarnation text;
begin
  old_incarnation := old.id::text || '/' || old.wake_id::text || '/' || old.node_id::text;
  if new.state in ('snapshotting','parked') and new.state is distinct from old.state
     and exists(select 1 from exclusive_work_operations where state='running'
       and incarnation_id=old_incarnation and lease_expires_at>clock_timestamp()
       and attempt_deadline>clock_timestamp()) then
    raise exception 'active exclusive operation prevents parking' using errcode='55000';
  end if;
  if new.wake_id is distinct from old.wake_id or new.node_id is distinct from old.node_id
     or (new.state is distinct from old.state and new.state<>'running') then
    update exclusive_work_operations set state='pending',claim_token=null,
      lease_expires_at=null,attempt_deadline=null,last_error='runtime incarnation revoked'
    where state='running' and incarnation_id=old_incarnation;
  end if;
  return new;
end;
$$;
drop trigger if exists exclusive_work_instance_transition on instances;
create trigger exclusive_work_instance_transition
  before update of state,wake_id,node_id on instances
  for each row execute function exclusive_work_instance_transition();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop trigger exclusive_work_instance_transition on instances;
drop function exclusive_work_instance_transition();
-- +goose StatementEnd
