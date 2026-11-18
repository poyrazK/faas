-- migration-compat: expand
-- filename: 20261117194056562_support_grant_request_scope.sql

-- +goose Up
-- Support access requests say what support asks for (ADR-20261116163508
-- amendment 1): the apps (NULL leaves the choice to the customer), how long,
-- and, for a request to extend access, the grant it continues. The customer
-- still decides the grant itself on approval.
-- support_grants.summary_sent_at marks a grant whose end-of-access summary
-- has been mailed to the organization's owners and admins.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE support_grant_requests ADD COLUMN IF NOT EXISTS app_ids uuid[];
ALTER TABLE support_grant_requests ADD COLUMN IF NOT EXISTS ttl_seconds integer;
ALTER TABLE support_grant_requests ADD COLUMN IF NOT EXISTS extends_grant_id uuid;
ALTER TABLE support_grants ADD COLUMN IF NOT EXISTS summary_sent_at timestamptz;
ALTER TABLE support_grant_requests ADD CONSTRAINT support_grant_requests_apps_check
    CHECK (app_ids IS NULL OR cardinality(app_ids) BETWEEN 1 AND 100) NOT VALID;
ALTER TABLE support_grant_requests ADD CONSTRAINT support_grant_requests_ttl_check
    CHECK (ttl_seconds IS NULL OR ttl_seconds BETWEEN 3600 AND 604800) NOT VALID;
ALTER TABLE support_grant_requests ADD CONSTRAINT support_grant_requests_extends_fkey
    FOREIGN KEY (extends_grant_id) REFERENCES support_grants(id) ON DELETE SET NULL NOT VALID;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
ALTER TABLE support_grant_requests DROP CONSTRAINT IF EXISTS support_grant_requests_extends_fkey;
ALTER TABLE support_grant_requests DROP CONSTRAINT IF EXISTS support_grant_requests_ttl_check;
ALTER TABLE support_grant_requests DROP CONSTRAINT IF EXISTS support_grant_requests_apps_check;
ALTER TABLE support_grants DROP COLUMN IF EXISTS summary_sent_at;
ALTER TABLE support_grant_requests DROP COLUMN IF EXISTS extends_grant_id;
ALTER TABLE support_grant_requests DROP COLUMN IF EXISTS ttl_seconds;
ALTER TABLE support_grant_requests DROP COLUMN IF EXISTS app_ids;
-- +goose StatementEnd
