-- +goose Up
-- +goose StatementBegin
ALTER TABLE apps
  ADD COLUMN IF NOT EXISTS cpu_millicores integer NOT NULL DEFAULT 1000;

ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_cpu_millicores_chk,
  ADD CONSTRAINT apps_cpu_millicores_chk
    CHECK (cpu_millicores IN (250, 500, 1000));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_cpu_millicores_chk,
  DROP COLUMN IF EXISTS cpu_millicores;
-- +goose StatementEnd
