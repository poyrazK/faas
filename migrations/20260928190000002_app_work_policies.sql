-- +goose Up
-- +goose StatementBegin
create table if not exists app_work_policies (
  app_id uuid not null references apps(id) on delete cascade,
  account_id uuid not null references accounts(id) on delete cascade,
  name text not null check (name ~ '^[a-z][a-z0-9-]{0,62}$'),
  revision bigint not null default 1 check (revision > 0),
  max_running_per_key int not null check (max_running_per_key = 1),
  pending_updates text not null check (pending_updates in ('all', 'keep_latest')),
  debounce_ms bigint not null check (debounce_ms between 0 and 86400000),
  expires_after_ms bigint not null check (expires_after_ms between 0 and 2592000000),
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  primary key (app_id, name)
);

alter table invocations add column if not exists work_policy_revision bigint
  check (work_policy_revision is null or work_policy_revision > 0);

do $$ begin
  if not exists (select 1 from pg_constraint
      where conname = 'event_subscriptions_id_app_uniq'
        and conrelid = 'event_subscriptions'::regclass) then
    alter table event_subscriptions add constraint event_subscriptions_id_app_uniq unique (id, app_id);
  end if;
end $$;
create table if not exists event_subscription_work_bindings (
  subscription_id uuid primary key,
  app_id uuid not null,
  policy_name text not null,
  key_selector text not null check (length(key_selector) between 1 and 256),
  foreign key (subscription_id, app_id)
    references event_subscriptions(id, app_id) on delete cascade,
  foreign key (app_id, policy_name)
    references app_work_policies(app_id, name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table event_subscription_work_bindings;
alter table event_subscriptions drop constraint event_subscriptions_id_app_uniq;
alter table invocations drop column work_policy_revision;
drop table app_work_policies;
-- +goose StatementEnd
