-- +goose Up
-- +goose StatementBegin
ALTER TABLE crons
    ADD COLUMN suspended_reason text NOT NULL DEFAULT '';

ALTER TABLE crons
    ADD CONSTRAINT crons_suspended_reason_chk
    CHECK (suspended_reason IN ('', 'no_live_deployment'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_suspended_reason_chk;
ALTER TABLE crons DROP COLUMN IF EXISTS suspended_reason;
-- +goose StatementEnd
