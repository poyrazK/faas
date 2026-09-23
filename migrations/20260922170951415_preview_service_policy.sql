-- filename: 20260922170951415_preview_service_policy.sql

-- +goose Up
-- +goose StatementBegin
-- Existing projects keep the behaviour that shipped before this policy:
-- preview callers are marked and allowed to reach production dependencies.
-- A missing policy row belongs to a project created after this migration and
-- resolves to the code default (`deny`), so new projects fail closed without
-- changing live traffic for existing customers.
alter table github_deploy_policies
    add column if not exists preview_service_policy text;

update github_deploy_policies
   set preview_service_policy = 'allow_marked'
 where preview_service_policy is null;

insert into github_deploy_policies
    (project_id, account_id, root_dir, ignored_paths, preview_enabled,
     preview_ttl_hours, preview_service_policy, updated_at)
select p.id, p.account_id, '', '[]'::jsonb, true, 168,
       'allow_marked', now()
  from projects p
on conflict (project_id) do nothing;

alter table github_deploy_policies
    alter column preview_service_policy set default 'deny',
    alter column preview_service_policy set not null;

do $$
begin
    if not exists (
        select 1
          from pg_constraint
         where conrelid = 'github_deploy_policies'::regclass
           and conname = 'github_deploy_policies_preview_service_policy_chk'
    ) then
        alter table github_deploy_policies
            add constraint github_deploy_policies_preview_service_policy_chk
            check (preview_service_policy in ('deny', 'allow_marked'));
    end if;
end
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
alter table github_deploy_policies
    drop constraint if exists github_deploy_policies_preview_service_policy_chk;

alter table github_deploy_policies
    drop column if exists preview_service_policy;
-- +goose StatementEnd
