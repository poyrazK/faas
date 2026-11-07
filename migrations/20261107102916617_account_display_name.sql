-- migration-compat: expand
-- filename: 20261107102916617_account_display_name.sql

-- +goose Up
-- +goose StatementBegin
-- Optional human name shown in org member lists and the dashboard
-- (ADR-20261107103000). Empty means unset. The CHECK is validated by
-- 20261107131859003_account_display_name_validate.sql.
SET LOCAL lock_timeout = '5s';
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS display_name text NOT NULL DEFAULT '';
ALTER TABLE accounts ADD CONSTRAINT accounts_display_name_check CHECK (
    char_length(display_name) <= 80
    AND display_name = btrim(display_name)
    AND display_name !~ '[[:cntrl:]]') NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE accounts DROP CONSTRAINT IF EXISTS accounts_display_name_check;
ALTER TABLE accounts DROP COLUMN IF EXISTS display_name;
-- +goose StatementEnd
