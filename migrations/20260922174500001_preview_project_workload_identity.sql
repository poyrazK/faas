-- +goose Up
-- +goose StatementBegin
-- Project previews intentionally retain their production workload identity so
-- service resolution can scope a workload name to one PR. The original index
-- treated that identity as globally unique inside the project, which made a
-- preview collide with its production parent. Keep the production invariant
-- while giving PR previews their own environment-scoped key.
drop index if exists apps_project_workload_uniq;
drop index if exists apps_preview_project_pr_workload_uniq;

create unique index apps_project_workload_uniq
    on apps (project_id, workload_name)
    where project_id is not null and preview_of_slug is null;

create unique index apps_preview_project_pr_workload_uniq
    on apps (project_id, preview_pr_number, workload_name)
    where project_id is not null
      and preview_of_slug is not null
      and preview_pr_number > 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop index if exists apps_preview_project_pr_workload_uniq;
drop index if exists apps_project_workload_uniq;

-- This deliberately fails loud if project previews still exist. Restoring the
-- old invariant while duplicate project/workload identities remain would be
-- unsafe; operators must tear down those previews before rolling back.
create unique index apps_project_workload_uniq
    on apps (project_id, workload_name)
    where project_id is not null;
-- +goose StatementEnd
