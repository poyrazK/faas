-- filename: 20260923160233763_preview_deleted_workload_identity.sql

-- +goose Up
-- +goose StatementBegin
-- A replaced PR dependency keeps its app row for deployment history. Deleted
-- rows must not reserve the project/PR workload identity forever: a later PR
-- head can add the same dependency back as a fresh preview app.
create unique index apps_preview_project_pr_workload_live_uniq
    on apps (project_id, preview_pr_number, workload_name)
    where project_id is not null
      and preview_of_slug is not null
      and preview_pr_number > 0
      and status <> 'deleted';
drop index apps_preview_project_pr_workload_uniq;
alter index apps_preview_project_pr_workload_live_uniq
    rename to apps_preview_project_pr_workload_uniq;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- This rolls back only if no PR workload has been retired and re-added. If
-- duplicates exist, the old index creation fails and the transaction restores
-- the live-only index instead of silently discarding history.
drop index apps_preview_project_pr_workload_uniq;
create unique index apps_preview_project_pr_workload_uniq
    on apps (project_id, preview_pr_number, workload_name)
    where project_id is not null
      and preview_of_slug is not null
      and preview_pr_number > 0;
-- +goose StatementEnd
