-- +goose Up
-- +goose StatementBegin

ALTER TABLE apps
  ADD COLUMN IF NOT EXISTS retry_policy jsonb NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_retry_policy_object_chk;
ALTER TABLE apps
  ADD CONSTRAINT apps_retry_policy_object_chk
  CHECK (jsonb_typeof(retry_policy) = 'object');

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_retry_policy_object_chk;
ALTER TABLE apps
  DROP COLUMN IF EXISTS retry_policy;

-- +goose StatementEnd
