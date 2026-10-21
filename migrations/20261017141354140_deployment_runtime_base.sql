-- +goose Up
-- The shared runner base a source-built container app's layer was built for;
-- schedd boots it instead of the app's own base
-- (docs/adr/20261017-shared-runtime-source-apps.md, H10-140).
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS runtime_base text NOT NULL DEFAULT ''
 CONSTRAINT deployments_runtime_base_check CHECK (runtime_base IN ('','node22','node24'));

-- +goose Down
ALTER TABLE deployments DROP COLUMN IF EXISTS runtime_base;
