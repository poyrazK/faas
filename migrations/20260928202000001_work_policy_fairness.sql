-- +goose Up
-- +goose StatementBegin
alter table app_work_policies
  add column max_running_per_fairness_key integer not null default 0
  check (max_running_per_fairness_key between 0 and 1000);

alter table invocations
  add column work_fairness_digest bytea,
  add column work_fairness_limit integer,
  add constraint invocations_work_fairness_check check (
    (work_fairness_digest is null and work_fairness_limit is null)
    or (work_policy_name is not null and work_fairness_digest is not null
        and work_fairness_limit is not null
        and length(work_fairness_digest) = 32
        and work_fairness_limit between 1 and 1000)
  );

create table invocation_work_fairness_lanes (
  app_id uuid not null references apps(id) on delete cascade,
  policy_name text not null check (policy_name ~ '^[a-z][a-z0-9-]{0,62}$'),
  fairness_digest bytea not null check (length(fairness_digest) = 32),
  primary key (app_id, policy_name, fairness_digest)
);

create index invocations_work_fairness_active_idx
  on invocations (app_id, work_policy_name, work_fairness_digest)
  where state = 'dispatching' and work_fairness_digest is not null;

alter table event_subscription_work_bindings
  add column fairness_key_selector text not null default '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table event_subscription_work_bindings drop column fairness_key_selector;
drop index invocations_work_fairness_active_idx;
drop table invocation_work_fairness_lanes;
alter table invocations drop constraint invocations_work_fairness_check;
alter table invocations drop column work_fairness_limit;
alter table invocations drop column work_fairness_digest;
alter table app_work_policies drop column max_running_per_fairness_key;
-- +goose StatementEnd
