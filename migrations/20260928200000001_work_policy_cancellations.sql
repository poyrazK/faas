-- +goose Up
-- +goose StatementBegin
create table invocation_work_cancellations (
  id uuid primary key,
  app_id uuid not null references apps(id) on delete cascade,
  policy_name text not null check (policy_name ~ '^[a-z][a-z0-9-]{0,62}$'),
  key_digest bytea not null check (length(key_digest) = 32),
  cancelled_count bigint not null default 0 check (cancelled_count >= 0),
  created_at timestamptz not null default now()
);

create index invocation_work_cancellations_app_created_idx
  on invocation_work_cancellations (app_id, created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table invocation_work_cancellations;
-- +goose StatementEnd
