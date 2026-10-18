-- Offline projection of migration-owned control-plane tables for sqlc.
CREATE TABLE org_memberships (
    org_id uuid NOT NULL, account_id uuid NOT NULL, role text NOT NULL,
    access_revision bigint NOT NULL, removed_at timestamptz,
    PRIMARY KEY (org_id, account_id)
);
CREATE TABLE api_keys (
    id uuid PRIMARY KEY, account_id uuid NOT NULL, org_id uuid NOT NULL,
    status text NOT NULL, expires_at timestamptz
);
CREATE TABLE deploy_tokens (
    id uuid PRIMARY KEY, account_id uuid NOT NULL, app_id uuid NOT NULL,
    status text NOT NULL, expires_at timestamptz
);
CREATE TABLE oidc_exchanged_tokens (
    id uuid PRIMARY KEY, account_id uuid NOT NULL, expires_at timestamptz NOT NULL
);
