-- filename: 20260930171612937_managed_exclusive_operations.sql

-- +goose Up
-- +goose StatementBegin
create table if not exists exclusive_work_policies (
  id uuid primary key,
  account_id uuid not null references accounts(id) on delete cascade,
  name text not null check (name ~ '^[a-z][a-z0-9-]{0,62}$'),
  revision bigint not null default 1 check (revision > 0),
  configuration jsonb not null check (jsonb_typeof(configuration) = 'object'),
  retired boolean not null default false,
  created_at timestamptz not null default clock_timestamp(),
  updated_at timestamptz not null default clock_timestamp(),
  unique (account_id, name),
  unique (id, account_id)
);

-- Never delete a key while its account exists: reusing a business key must
-- never make a historic ownership generation authoritative again.
create table if not exists exclusive_work_keys (
  id uuid primary key,
  account_id uuid not null references accounts(id) on delete cascade,
  policy_id uuid not null,
  scope_id uuid not null,
  environment_id text not null default '',
  key_digest bytea not null check (length(key_digest) = 32),
  generation bigint not null default 0 check (generation >= 0),
  next_sequence bigint not null default 1 check (next_sequence > 0),
  foreign key (policy_id, account_id) references exclusive_work_policies(id, account_id),
  unique (policy_id, scope_id, environment_id, key_digest),
  unique (id, account_id)
);

create table if not exists exclusive_work_operations (
  id uuid primary key,
  account_id uuid not null references accounts(id) on delete cascade,
  key_id uuid not null,
  app_id uuid not null references apps(id),
  platform_tenant_id uuid references platform_tenants(id),
  sequence bigint not null check (sequence > 0),
  state text not null default 'pending'
    check (state in ('pending','running','completed','failed','cancelled','expired')),
  policy_revision bigint not null check (policy_revision > 0),
  configuration jsonb not null check (jsonb_typeof(configuration) = 'object'),
  request jsonb not null check (jsonb_typeof(request) = 'object'),
  request_digest bytea not null check (length(request_digest) = 32),
  equivalence_digest bytea check (equivalence_digest is null or length(equivalence_digest) = 32),
  idempotency_digest bytea check (idempotency_digest is null or length(idempotency_digest) = 32),
  generation bigint not null default 0 check (generation >= 0),
  claim_token uuid,
  incarnation_id text not null default '',
  lease_expires_at timestamptz,
  attempt_deadline timestamptz,
  result jsonb,
  last_error text not null default '',
  created_at timestamptz not null default clock_timestamp(),
  completed_at timestamptz,
  foreign key (key_id, account_id) references exclusive_work_keys(id, account_id),
  unique (key_id, sequence),
  check ((state = 'running' and generation > 0 and claim_token is not null
          and incarnation_id <> '' and lease_expires_at is not null and attempt_deadline is not null)
         or (state <> 'running' and claim_token is null and lease_expires_at is null
             and attempt_deadline is null))
);

create unique index if not exists exclusive_work_idempotency_idx
  on exclusive_work_operations (key_id, idempotency_digest)
  where idempotency_digest is not null;
create unique index if not exists exclusive_work_one_owner_idx
  on exclusive_work_operations (key_id) where state = 'running';
create index if not exists exclusive_work_pending_idx
  on exclusive_work_operations (key_id, sequence) where state in ('pending','running');
create index if not exists exclusive_work_expired_owner_idx
  on exclusive_work_operations (lease_expires_at) where state = 'running';

-- A joined submission keeps its own retry receipt after the original finishes.
create table if not exists exclusive_work_submissions (
  key_id uuid not null references exclusive_work_keys(id),
  idempotency_digest bytea not null check (length(idempotency_digest) = 32),
  operation_id uuid not null references exclusive_work_operations(id),
  primary key (key_id, idempotency_digest)
);

-- Platform-controlled effects are inserted with the result under the same
-- ownership lock. Consumers deliver them at least once with a stable ID.
create table if not exists exclusive_work_effects (
  id uuid primary key,
  operation_id uuid not null references exclusive_work_operations(id),
  generation bigint not null check (generation > 0),
  name text not null check (name ~ '^[a-z][a-z0-9-]{0,62}$'),
  payload jsonb not null,
  created_at timestamptz not null default clock_timestamp(),
  unique (operation_id, generation, name)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table exclusive_work_effects;
drop table exclusive_work_submissions;
drop table exclusive_work_operations;
drop table exclusive_work_keys;
drop table exclusive_work_policies;
-- +goose StatementEnd
