-- +goose Up
-- +goose StatementBegin

-- App security posture starts advisory/off for existing and newly-created
-- apps. Operators can opt an app into warn or enforce through the
-- admin+MFA security surface.
ALTER TABLE apps
  ADD COLUMN IF NOT EXISTS security_policy text NOT NULL DEFAULT 'off';

ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_security_policy_chk;
ALTER TABLE apps
  ADD CONSTRAINT apps_security_policy_chk
  CHECK (security_policy IN ('off', 'warn', 'enforce'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

ALTER TABLE apps
  DROP CONSTRAINT IF EXISTS apps_security_policy_chk;
ALTER TABLE apps
  DROP COLUMN IF EXISTS security_policy;

-- +goose StatementEnd
