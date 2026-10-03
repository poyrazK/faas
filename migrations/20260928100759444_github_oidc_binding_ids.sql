-- filename: 20260928100759444_github_oidc_binding_ids.sql

-- +goose Up
-- +goose StatementBegin
alter table apps
  add column if not exists github_owner_id bigint,
  add column if not exists github_repo_id bigint;

do $$
begin
  if not exists (
    select 1 from pg_constraint
     where conname = 'apps_github_identity_ids_check'
       and conrelid = 'apps'::regclass
  ) then
    alter table apps add constraint apps_github_identity_ids_check
      check ((github_owner_id is null and github_repo_id is null)
          or (github_owner_id is not null and github_repo_id is not null
              and github_owner_id > 0 and github_repo_id > 0));
  end if;
end
$$;

create index if not exists apps_github_oidc_identity_idx
  on apps (github_owner_id, github_repo_id)
  where github_install_id is not null
    and github_owner_id is not null
    and github_repo_id is not null;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists apps_github_oidc_identity_idx;
alter table apps
  drop constraint if exists apps_github_identity_ids_check,
  drop column if exists github_repo_id,
  drop column if exists github_owner_id;
-- +goose StatementEnd
