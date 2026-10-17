-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS hosting_oidc_trusts (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
 site_id uuid NOT NULL REFERENCES hosting_sites(id) ON DELETE CASCADE,
 repository_owner_id text NOT NULL CHECK (repository_owner_id ~ '^[1-9][0-9]{0,19}$'),
 repository_id text NOT NULL CHECK (repository_id ~ '^[1-9][0-9]{0,19}$'),
 repository text NOT NULL CHECK (octet_length(repository) BETWEEN 3 AND 200),
 ref text NOT NULL CHECK (octet_length(ref) BETWEEN 12 AND 255 AND ref LIKE 'refs/heads/%'),
 workflow_ref text NOT NULL CHECK (octet_length(workflow_ref) BETWEEN 1 AND 512),
 event_name text NOT NULL CHECK (event_name IN ('push','workflow_dispatch','schedule')),
 environment text NOT NULL DEFAULT '' CHECK (octet_length(environment)<=255),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
 UNIQUE(account_id,site_id,id)
);
CREATE INDEX IF NOT EXISTS hosting_oidc_trusts_site ON hosting_oidc_trusts(site_id);
ALTER TABLE hosting_automation_credentials ADD COLUMN IF NOT EXISTS oidc_trust_id uuid;
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'hosting_automation_credentials'::regclass AND conname = 'hosting_credential_oidc_trust') THEN
        ALTER TABLE hosting_automation_credentials ADD CONSTRAINT hosting_credential_oidc_trust
            FOREIGN KEY(account_id,site_id,oidc_trust_id) REFERENCES hosting_oidc_trusts(account_id,site_id,id) ON DELETE CASCADE;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS hosting_credentials_oidc_trust ON hosting_automation_credentials(oidc_trust_id) WHERE oidc_trust_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM hosting_oidc_trusts) THEN
  RAISE EXCEPTION 'Revoke hosting OIDC trusts before downgrade';
 END IF;
END $$;
ALTER TABLE hosting_automation_credentials DROP COLUMN oidc_trust_id;
DROP TABLE hosting_oidc_trusts;
-- +goose StatementEnd
