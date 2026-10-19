-- migration-compat: expand
-- migration-lint: allow index-build managed_realtime_endpoints: an account holds at most 250 endpoints (RealtimeLimits.EndpointsPerAccount), and the partial index covers only rows with the new slug, which every existing row lacks
-- +goose Up
-- ADR-20261019102657: an endpoint may carry a slug, unique within its app, so
-- gregale.yaml can declare it and reconcile it across deploys and accounts.
-- API-created endpoints may stay without one; the previous release never
-- reads or writes the column.
SET LOCAL lock_timeout = '5s';
ALTER TABLE managed_realtime_endpoints
    ADD COLUMN IF NOT EXISTS slug text
    CHECK (slug IS NULL OR slug ~ '^[a-z0-9][a-z0-9-]{0,47}$');
CREATE UNIQUE INDEX IF NOT EXISTS managed_realtime_endpoints_app_slug
    ON managed_realtime_endpoints (app_id, slug) WHERE slug IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS managed_realtime_endpoints_app_slug;
ALTER TABLE managed_realtime_endpoints DROP COLUMN IF EXISTS slug;
