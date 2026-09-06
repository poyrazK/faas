-- +goose Up
-- Per-project GitHub branch to deployment-scope routing.
-- A missing row preserves the legacy production-branch -> default scope
-- behaviour; rows for additional branches opt those branches into deploys.
create table if not exists github_deploy_branches (
    project_id uuid not null references projects(id) on delete cascade,
    branch text not null,
    scope text not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    primary key (project_id, branch),
    check (length(branch) between 1 and 255),
    check (branch !~ '[[:cntrl:]]'),
    check (scope ~ '^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$')
);

create index if not exists github_deploy_branches_project_idx
    on github_deploy_branches (project_id);

-- +goose Down
drop table if exists github_deploy_branches;
