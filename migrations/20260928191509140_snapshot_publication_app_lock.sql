-- filename: 20260928191509140_snapshot_publication_app_lock.sql

-- +goose Up
-- +goose StatementBegin
-- A config stamp and a snapshot publication must order through the same app
-- row. If publication wins, the following invalidation scan can mark its row
-- stale. If the stamp wins, publication sees it and rejects an older guest.
-- Cover direct SQL stamp writers as well as MarkAppRuntimeConfigChanged.
create or replace function app_runtime_config_change_lock_app() returns trigger
language plpgsql as $$
begin
    perform 1 from apps where id = new.app_id for update;
    return new;
end;
$$;

drop trigger if exists app_runtime_config_change_lock_app on app_runtime_config_changes;
create trigger app_runtime_config_change_lock_app
before insert or update on app_runtime_config_changes
for each row execute function app_runtime_config_change_lock_app();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop trigger if exists app_runtime_config_change_lock_app on app_runtime_config_changes;
drop function if exists app_runtime_config_change_lock_app();
-- +goose StatementEnd
