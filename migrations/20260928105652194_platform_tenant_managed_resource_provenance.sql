-- filename: 20260928105652194_platform_tenant_managed_resource_provenance.sql

-- +goose Up
-- +goose StatementBegin
-- New resources created by an account owner's additive tenant bundle apply are
-- safe candidates for a future declarative reconciliation flow. Existing or
-- separately created resources remain unmanaged; never infer ownership from a
-- tenant link or a matching name.
ALTER TABLE api_consumers
  ADD COLUMN IF NOT EXISTS platform_tenant_managed boolean NOT NULL DEFAULT false;

ALTER TABLE tenant_surfaces
  ADD COLUMN IF NOT EXISTS platform_tenant_managed boolean NOT NULL DEFAULT false;

ALTER TABLE tenant_hostnames
  ADD COLUMN IF NOT EXISTS platform_tenant_managed boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE tenant_hostnames DROP COLUMN IF EXISTS platform_tenant_managed;
ALTER TABLE tenant_surfaces DROP COLUMN IF EXISTS platform_tenant_managed;
ALTER TABLE api_consumers DROP COLUMN IF EXISTS platform_tenant_managed;
-- +goose StatementEnd
