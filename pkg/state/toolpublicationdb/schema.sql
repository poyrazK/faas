CREATE TABLE accounts (id uuid PRIMARY KEY);
CREATE TABLE api_keys (id uuid PRIMARY KEY, account_id uuid NOT NULL, status text NOT NULL, expires_at timestamptz, scopes text[] NOT NULL, label text NOT NULL, org_id uuid);
CREATE TABLE apps (id uuid PRIMARY KEY, account_id uuid NOT NULL, project_id uuid, org_id uuid, slug text NOT NULL);
CREATE TABLE orgs (id uuid PRIMARY KEY, personal_org boolean NOT NULL, personal_owner_account_id uuid);
CREATE TABLE api_key_project_limits (key_id uuid PRIMARY KEY, project_ids uuid[] NOT NULL);
CREATE TABLE tool_publications (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id text NOT NULL,
    status text NOT NULL CHECK (status IN ('running','completed','failed','outcome_unknown')),
    payload jsonb NOT NULL,
    PRIMARY KEY (account_id, id)
);
CREATE TABLE tool_invocations (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('queued','running','completed','tool_error','not_started','cancelled','outcome_unknown')),
    deadline timestamptz NOT NULL,
    payload jsonb NOT NULL,
    PRIMARY KEY (account_id,id)
);
CREATE TABLE tool_grants (
    account_id uuid NOT NULL REFERENCES accounts(id),
    id uuid NOT NULL,
    key_id uuid NOT NULL REFERENCES api_keys(id),
    version text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    payload jsonb NOT NULL,
    PRIMARY KEY (account_id,id)
);

CREATE TABLE app_secrets (account_id uuid NOT NULL, app_id uuid NOT NULL, scope text NOT NULL, key text NOT NULL, ciphertext bytea NOT NULL, managed_postgres_binding_id uuid, managed_object_storage_credential_id uuid, managed_credential_ref text);

CREATE TABLE tool_oauth_connections (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id uuid NOT NULL,
    app_id uuid NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
    payload jsonb NOT NULL,
    state text NOT NULL,
    generation bigint NOT NULL DEFAULT 0,
    credentials_ciphertext bytea,
    expires_at timestamptz,
    lease_id uuid,
    lease_until timestamptz,
    revoked_at timestamptz,
    PRIMARY KEY (account_id,id)
);

CREATE TABLE tool_oauth_authorizations (
    account_id uuid NOT NULL,
    connection_id uuid NOT NULL,
    state_sha256 text NOT NULL,
    phase text NOT NULL,
    expires_at timestamptz NOT NULL,
    flow_ciphertext bytea,
    PRIMARY KEY(account_id,connection_id),
    FOREIGN KEY(account_id,connection_id) REFERENCES tool_oauth_connections(account_id,id) ON DELETE CASCADE
);
