-- +goose Up
-- +goose StatementBegin
-- A claimed teardown cannot be reopened, but remains visible to the janitor
-- after a crash or cleanup failure until it reaches torn_down.
alter table apps drop constraint apps_preview_pr_state_chk;
alter table apps add constraint apps_preview_pr_state_chk
    check (preview_pr_state in ('open','closed','stale','tearing_down','torn_down')
           or preview_pr_state is null);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
update apps set preview_pr_state = 'stale' where preview_pr_state = 'tearing_down';
alter table apps drop constraint apps_preview_pr_state_chk;
alter table apps add constraint apps_preview_pr_state_chk
    check (preview_pr_state in ('open','closed','stale','torn_down')
           or preview_pr_state is null);
-- +goose StatementEnd
