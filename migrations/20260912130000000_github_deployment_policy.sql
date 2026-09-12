-- +goose Up
-- Customer-owned GitHub deployment policy. The row is optional: an absent
-- policy preserves the legacy githubd defaults.
create table if not exists github_deploy_policies (
    project_id       uuid primary key references projects(id) on delete cascade,
    account_id       uuid not null references accounts(id) on delete cascade,
    root_dir         text not null default '',
    ignored_paths     jsonb not null default '[]'::jsonb,
    preview_enabled   boolean not null default true,
    preview_ttl_hours integer not null default 168,
    updated_at        timestamptz not null default now(),
    constraint github_deploy_policies_root_dir_chk check (
        length(root_dir) <= 255 and root_dir !~ '[[:cntrl:]]'
    ),
    constraint github_deploy_policies_ignored_paths_array_chk check (
        jsonb_typeof(ignored_paths) = 'array'
    ),
    constraint github_deploy_policies_preview_ttl_chk check (
        preview_ttl_hours between 1 and 720
    )
);

create index if not exists github_deploy_policies_account_idx
    on github_deploy_policies (account_id);

-- +goose Down
drop table if exists github_deploy_policies;
