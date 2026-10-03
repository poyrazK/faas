-- A soft-deleted app keeps its crons suspended with reason 'app_deleted'
-- instead of deleting them, so restoring the app inside its grace window
-- brings its schedules back. The purge still removes them with the app.

-- +goose Up
-- +goose StatementBegin
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_suspended_reason_chk;
ALTER TABLE crons
    ADD CONSTRAINT crons_suspended_reason_chk
    CHECK (suspended_reason IN ('', 'no_live_deployment', 'app_deleted'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM crons WHERE suspended_reason = 'app_deleted';
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_suspended_reason_chk;
ALTER TABLE crons
    ADD CONSTRAINT crons_suspended_reason_chk
    CHECK (suspended_reason IN ('', 'no_live_deployment'));
-- +goose StatementEnd
