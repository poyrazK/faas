-- +goose Up
-- +goose StatementBegin

-- Initial schema (spec §5). Authored here; sqlc generates typed queries against
-- it. Every state column carries a CHECK constraint; every table with account_id
-- gets a composite index leading with it. Money is integer millicents elsewhere.
-- Never edit this migration after merge — append a new one.

create extension if not exists citext;

create table accounts (
  id uuid primary key default gen_random_uuid(),
  email citext unique not null,
  plan text not null default 'free' check (plan in ('free','hobby','pro','scale')),
  status text not null default 'active' check (status in ('active','past_due','suspended','deleted_pending')),
  stripe_customer_id text unique,
  created_at timestamptz not null default now()
);

create table api_keys (
  id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(id) on delete cascade,
  key_sha256 bytea unique not null,
  label text,
  last_used_at timestamptz,
  created_at timestamptz not null default now()
);
create index api_keys_account_idx on api_keys (account_id);

create table apps (
  id uuid primary key default gen_random_uuid(),
  account_id uuid not null references accounts(id),
  slug text unique not null,
  type text not null default 'app' check (type in ('app','function')),
  runtime text check (runtime is null or runtime in ('node22','python312')),
  ram_mb int not null check (ram_mb > 0),
  idle_timeout_s int check (idle_timeout_s is null or idle_timeout_s >= 10),
  max_concurrency int not null default 1 check (max_concurrency >= 1),
  status text not null default 'active' check (status in ('active','evicted_cold','deleted')),
  created_at timestamptz not null default now()
);
create index apps_account_idx on apps (account_id, status);

create table deployments (
  id uuid primary key default gen_random_uuid(),
  app_id uuid not null references apps(id),
  build_id uuid,
  image_digest text not null,
  rootfs_path text,
  rootfs_bytes bigint,
  status text not null check (status in ('pending','building','imaging','snapshotting','live','failed','superseded')),
  error text,
  created_at timestamptz not null default now()
);
create index deployments_app_idx on deployments (app_id, created_at desc);

create table builds (
  id uuid primary key default gen_random_uuid(),
  deployment_id uuid not null references deployments(id),
  kind text not null check (kind in ('railpack','dockerfile')),
  source_bytes bigint not null,
  status text not null check (status in ('queued','running','succeeded','failed')),
  failure_class text check (failure_class is null or failure_class in ('oom','timeout','user_error','infra')),
  log_path text,
  started_at timestamptz,
  finished_at timestamptz
);

create table snapshots (
  id uuid primary key default gen_random_uuid(),
  deployment_id uuid not null references deployments(id),
  fc_version text not null,
  mem_bytes bigint not null,
  disk_bytes bigint not null,
  path text not null,
  stale bool not null default false,
  created_at timestamptz not null default now()
);
create index snapshots_deployment_idx on snapshots (deployment_id);

create table instances (
  id uuid primary key default gen_random_uuid(),
  app_id uuid not null references apps(id),
  deployment_id uuid not null references deployments(id),
  state text not null check (state in ('parked','waking','cold_booting','running','snapshotting','stopped','failed')),
  netns text,
  guest_uid int,
  host_ip inet,
  ram_mb int not null,
  started_at timestamptz,
  last_request_at timestamptz,
  parked_at timestamptz
);
create index instances_app_idx on instances (app_id, state);

create table usage_minutes (
  account_id uuid not null,
  app_id uuid not null,
  instance_id uuid not null,
  minute timestamptz not null,
  mb_seconds bigint not null,
  requests int not null default 0,
  primary key (instance_id, minute)
);

create table custom_domains (
  domain citext primary key,
  app_id uuid not null references apps(id),
  verified_at timestamptz
);

create table crons (
  id uuid primary key default gen_random_uuid(),
  app_id uuid not null references apps(id),
  schedule text not null,
  path text not null default '/',
  enabled bool not null default true
);

create table idempotency_keys (
  key text not null,
  account_id uuid not null references accounts(id),
  response_status int not null,
  response_body bytea not null,
  created_at timestamptz not null default now(),
  primary key (account_id, key)
);

create table events (
  id bigint generated always as identity primary key,
  at timestamptz not null default now(),
  actor text not null,
  kind text not null,
  subject uuid,
  data jsonb
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists events, idempotency_keys, crons, custom_domains, usage_minutes,
  instances, snapshots, builds, deployments, apps, api_keys, accounts cascade;
-- +goose StatementEnd
