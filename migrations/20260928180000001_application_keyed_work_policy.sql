-- +goose Up
-- +goose StatementBegin
alter table invocations
  add column work_policy_name text,
  add column work_key_digest bytea,
  add column work_expires_at timestamptz,
  add column work_sequence bigint,
  add constraint invocations_work_lane_check check (
    (work_policy_name is null and work_key_digest is null
     and work_expires_at is null and work_sequence is null)
    or (work_policy_name ~ '^[a-z][a-z0-9-]{0,62}$' and work_key_digest is not null
        and length(work_key_digest) = 32 and work_sequence > 0)
  );

create table invocation_work_lanes (
  app_id uuid not null references apps(id) on delete cascade,
  policy_name text not null check (policy_name ~ '^[a-z][a-z0-9-]{0,62}$'),
  key_digest bytea not null check (length(key_digest) = 32),
  next_sequence bigint not null default 1 check (next_sequence > 0),
  primary key (app_id, policy_name, key_digest)
);

create index invocations_work_lane_active_idx
  on invocations (app_id, work_policy_name, work_key_digest, work_sequence)
  where work_policy_name is not null and state in ('pending', 'dispatching');

create index invocations_work_expiring_idx
  on invocations (work_expires_at)
  where work_policy_name is not null and state = 'pending' and work_expires_at is not null;

alter table invocations drop constraint invocations_state_check;
alter table invocations add constraint invocations_state_check
  check (state in ('pending', 'dispatching', 'completed', 'failed',
                  'cancelled', 'dead_letter', 'superseded', 'expired'));
alter table invocations drop constraint invocations_outcome_check;
alter table invocations add constraint invocations_outcome_check
  check (outcome is null or outcome in ('success', 'failed', 'timeout',
                                       'dead_letter', 'superseded', 'expired'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
update invocations set state = 'cancelled', outcome = null
  where state in ('superseded', 'expired');
alter table invocations drop constraint invocations_outcome_check;
alter table invocations add constraint invocations_outcome_check
  check (outcome is null or outcome in ('success', 'failed', 'timeout', 'dead_letter'));
alter table invocations drop constraint invocations_state_check;
alter table invocations add constraint invocations_state_check
  check (state in ('pending', 'dispatching', 'completed', 'failed',
                  'cancelled', 'dead_letter'));
drop index invocations_work_lane_active_idx;
drop index invocations_work_expiring_idx;
drop table invocation_work_lanes;
alter table invocations drop constraint invocations_work_lane_check;
alter table invocations drop column work_sequence;
alter table invocations drop column work_expires_at;
alter table invocations drop column work_key_digest;
alter table invocations drop column work_policy_name;
-- +goose StatementEnd
