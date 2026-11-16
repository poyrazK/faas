-- migration-compat: expand
-- filename: 20261116193733100_support_grants.sql

-- +goose Up
-- Customer-granted support access (ADR-20261116163508).
--
-- support_grant_requests: a platform operator asks an organization (or a
-- personal account's organization) for access. A request sets nothing; it
-- expires after 24 hours.
-- support_grants: an owner or admin lets Gregale support deploy, roll back
-- and restart the organization's apps (all of them, or app_ids) until
-- expires_at, at most 7 days. A grant is usable only while it is unrevoked,
-- unexpired and its creator is still an active owner or admin; the
-- org-access fence re-checks that on every write.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE TABLE IF NOT EXISTS support_grant_requests (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    operator_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    reason text NOT NULL,
    status text NOT NULL DEFAULT 'open',
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    decided_at timestamptz,
    decided_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT support_grant_requests_pkey PRIMARY KEY (id),
    CONSTRAINT support_grant_requests_reason_check CHECK (char_length(reason) BETWEEN 3 AND 400),
    CONSTRAINT support_grant_requests_status_check CHECK (status IN ('open', 'approved', 'declined')),
    CONSTRAINT support_grant_requests_expiry_check CHECK (expires_at > created_at AND expires_at <= created_at + interval '24 hours'),
    CONSTRAINT support_grant_requests_decision_check CHECK ((status = 'open') = (decided_at IS NULL))
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS support_grant_requests_open_idx
    ON support_grant_requests (org_id, created_at DESC) WHERE status = 'open';
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS support_grants (
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    app_ids uuid[],
    created_by_account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    request_id uuid REFERENCES support_grant_requests(id) ON DELETE SET NULL,
    reason text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
    CONSTRAINT support_grants_pkey PRIMARY KEY (id),
    CONSTRAINT support_grants_reason_check CHECK (char_length(reason) BETWEEN 3 AND 400),
    CONSTRAINT support_grants_apps_check CHECK (app_ids IS NULL OR cardinality(app_ids) BETWEEN 1 AND 100),
    CONSTRAINT support_grants_expiry_check CHECK (expires_at > created_at AND expires_at <= created_at + interval '7 days'),
    CONSTRAINT support_grants_revoked_check CHECK (revoked_by_account_id IS NULL OR revoked_at IS NOT NULL)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS support_grants_org_idx ON support_grants (org_id, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  GRANT SELECT, INSERT, UPDATE ON support_grant_requests TO faas_runtime;
  GRANT SELECT, INSERT, UPDATE ON support_grants TO faas_runtime;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS support_grants;
DROP TABLE IF EXISTS support_grant_requests;
-- +goose StatementEnd
