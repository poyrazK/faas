-- +goose Up
-- +goose StatementBegin
ALTER TABLE crons
    ADD COLUMN IF NOT EXISTS suspended_reason text NOT NULL DEFAULT '';

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'crons'::regclass
          AND conname = 'crons_suspended_reason_chk'
    ) THEN
        ALTER TABLE crons
            ADD CONSTRAINT crons_suspended_reason_chk
            CHECK (suspended_reason IN ('', 'no_live_deployment'));
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE crons DROP CONSTRAINT IF EXISTS crons_suspended_reason_chk;
ALTER TABLE crons DROP COLUMN IF EXISTS suspended_reason;
-- +goose StatementEnd
