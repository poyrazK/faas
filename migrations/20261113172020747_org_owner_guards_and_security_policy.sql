-- migration-compat: expand
-- filename: 20261113172020747_org_owner_guards_and_security_policy.sql

-- +goose Up
-- Organization owners stay usable, and organizations can require two-factor
-- authentication (ADR-20261113180000).
--
-- 1. Only an active (or past-due) account can become an organization's
--    owner. An ownership transfer could hand an organization to an account
--    already pending deletion or suspended; org_app_identity_erasure_blocked
--    then kept that account's deletion from ever completing and left the
--    organization owned by an account nobody could use.
-- 2. org_security_policies holds an organization's require_mfa setting.
--    The org-access snapshot reads it, so a member without two-factor
--    authentication is not admitted to an organization that requires it.
-- Every step is re-runnable.
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
CREATE OR REPLACE FUNCTION guard_org_owner_account_active() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.role = 'owner' AND NEW.removed_at IS NULL AND NOT EXISTS (
        SELECT 1 FROM accounts a WHERE a.id = NEW.account_id AND a.status IN ('active', 'past_due')
    ) THEN
        RAISE EXCEPTION 'only an active account can own an organization'
            USING ERRCODE = '23514', CONSTRAINT = 'org_owner_account_inactive';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS org_memberships_owner_account_active ON org_memberships;
CREATE TRIGGER org_memberships_owner_account_active
    BEFORE INSERT OR UPDATE OF role, removed_at ON org_memberships FOR EACH ROW
    WHEN (NEW.role = 'owner')
    EXECUTE FUNCTION guard_org_owner_account_active();
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS org_security_policies (
    org_id uuid NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    require_mfa boolean NOT NULL DEFAULT false,
    updated_by_account_id uuid REFERENCES accounts(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT org_security_policies_pkey PRIMARY KEY (org_id),
    CONSTRAINT org_security_policies_updated_at_check CHECK (isfinite(updated_at))
);
-- +goose StatementEnd
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname='faas_runtime') THEN
  GRANT SELECT, INSERT, UPDATE ON org_security_policies TO faas_runtime;
 END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS org_security_policies;
DROP TRIGGER IF EXISTS org_memberships_owner_account_active ON org_memberships;
DROP FUNCTION IF EXISTS guard_org_owner_account_active();
-- +goose StatementEnd
