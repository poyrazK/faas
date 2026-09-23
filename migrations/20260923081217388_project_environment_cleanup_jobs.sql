-- filename: 20260923081217388_project_environment_cleanup_jobs.sql

-- +goose Up
-- +goose StatementBegin
create table if not exists project_environment_cleanup_jobs (
    id uuid primary key default gen_random_uuid(),
    account_id uuid not null,
    project_id uuid not null,
    environment_slug text not null,
    resources jsonb not null,
    attempt_count integer not null default 0 check (attempt_count >= 0),
    next_attempt_at timestamptz not null default now(),
    lease_token text not null default '',
    lease_until timestamptz,
    created_at timestamptz not null default now(),
    constraint project_environment_cleanup_jobs_resources_object
        check (jsonb_typeof(resources) = 'object')
);

create index if not exists project_environment_cleanup_jobs_due_idx
    on project_environment_cleanup_jobs (next_attempt_at, created_at, id)
    where lease_until is null;

create index if not exists project_environment_cleanup_jobs_lease_idx
    on project_environment_cleanup_jobs (lease_until)
    where lease_until is not null;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists project_environment_cleanup_jobs;
-- +goose StatementEnd
