-- +goose Up
-- +goose StatementBegin
-- The GitHub check is one result per commit, while a project preview may have
-- several deployments. Persist the exact app set selected from the PR head so
-- a single successful sibling cannot mark the environment ready.
create table if not exists pr_preview_sets (
    installation_id bigint not null check (installation_id > 0),
    repo_full_name text not null check (repo_full_name <> ''),
    pr_number integer not null check (pr_number > 0),
    commit_sha text not null check (commit_sha ~ '^[0-9a-f]{7,64}$'),
    root_app_id uuid not null references apps(id) on delete cascade,
    member_app_ids text[] not null check (cardinality(member_app_ids) > 0),
    closed_at timestamptz,
    updated_at timestamptz not null default now(),
    primary key (installation_id, repo_full_name, pr_number)
);
create index if not exists pr_preview_sets_root_idx on pr_preview_sets (root_app_id);
create index if not exists pr_preview_sets_members_idx on pr_preview_sets using gin (member_app_ids);

-- Preview apps are soft-deleted by the janitor, so the FK's ON DELETE action
-- alone would retain one set per old PR forever. Remove the set when its root
-- is tombstoned; late deployment notifications then become no-ops.
create or replace function prune_pr_preview_set_on_root_delete() returns trigger
language plpgsql as $$
begin
    delete from pr_preview_sets where root_app_id = new.id;
    return new;
end;
$$;
do $$
begin
    if not exists (
        select 1 from pg_trigger
        where tgrelid = 'apps'::regclass
          and tgname = 'prune_pr_preview_set_on_root_delete'
          and not tgisinternal
    ) then
        create trigger prune_pr_preview_set_on_root_delete
            after update of status on apps
            for each row
            when (old.status is distinct from 'deleted' and new.status = 'deleted')
            execute function prune_pr_preview_set_on_root_delete();
    end if;
end;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop trigger if exists prune_pr_preview_set_on_root_delete on apps;
drop function if exists prune_pr_preview_set_on_root_delete();
drop table if exists pr_preview_sets;
-- +goose StatementEnd
