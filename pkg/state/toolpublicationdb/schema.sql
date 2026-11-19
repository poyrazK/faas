CREATE TABLE accounts (id uuid PRIMARY KEY, status text NOT NULL);
CREATE TABLE task_tool_release_intents (
    account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    id text NOT NULL,
    status text NOT NULL,
    payload jsonb NOT NULL,
    PRIMARY KEY(account_id,id)
);
CREATE TABLE api_keys (id uuid PRIMARY KEY, account_id uuid NOT NULL, status text NOT NULL, expires_at timestamptz, scopes text[] NOT NULL, label text NOT NULL, org_id uuid);
CREATE TABLE apps (id uuid PRIMARY KEY, account_id uuid NOT NULL, project_id uuid, org_id uuid, slug text NOT NULL, status text NOT NULL, manifest jsonb NOT NULL, workload_class text NOT NULL, park_transition_id uuid);
CREATE TABLE orgs (id uuid PRIMARY KEY, personal_org boolean NOT NULL, personal_owner_account_id uuid);
CREATE TABLE api_key_project_limits (key_id uuid PRIMARY KEY, project_ids uuid[] NOT NULL);
CREATE TABLE deployments (id uuid PRIMARY KEY, app_id uuid NOT NULL, source_sha256 text, scope text, status text NOT NULL, traffic_percent integer NOT NULL, created_at timestamptz NOT NULL);
CREATE TABLE deployment_mcp_readiness_evidence (deployment_id uuid NOT NULL, role text NOT NULL, evidence jsonb NOT NULL);
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

-- Read-only recovery subjects; schedd remains the only instance writer.
CREATE TABLE instances (id uuid PRIMARY KEY, app_id uuid NOT NULL, deployment_id uuid, state text NOT NULL);
CREATE TABLE app_custom_metrics (app_id uuid NOT NULL, name text NOT NULL, value double precision NOT NULL, observed_at timestamptz NOT NULL, PRIMARY KEY(app_id,name));

CREATE TABLE hosted_task_source_intents(account_id uuid NOT NULL REFERENCES accounts(id),id text NOT NULL,payload jsonb NOT NULL,ciphertext bytea,expires_at timestamptz NOT NULL,PRIMARY KEY(account_id,id));

CREATE TABLE hosted_task_binding_qualifications(account_id uuid NOT NULL,id text NOT NULL,payload jsonb NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(account_id,id));
ALTER TABLE app_secrets ADD COLUMN secret_version bigint NOT NULL DEFAULT 0;
ALTER TABLE app_secrets ADD COLUMN managed_credential_generation bigint;
CREATE TABLE managed_postgres_bindings(id uuid PRIMARY KEY,account_id uuid NOT NULL,database_account_id uuid NOT NULL,database_id uuid NOT NULL,app_id uuid NOT NULL,scope text NOT NULL,environment_key text NOT NULL,credential_ref text,credential_generation bigint NOT NULL,state text NOT NULL,access text NOT NULL,deleted_at timestamptz);
CREATE TABLE app_envs(account_id uuid NOT NULL,app_id uuid NOT NULL,scope text NOT NULL,key text NOT NULL,value text NOT NULL,PRIMARY KEY(app_id,scope,key));
CREATE TABLE managed_postgres_databases(id uuid PRIMARY KEY,account_id uuid NOT NULL,state text NOT NULL);

CREATE TABLE hosted_task_rehearsal_plans(account_id uuid NOT NULL,id text NOT NULL,payload jsonb NOT NULL,expires_at timestamptz NOT NULL,PRIMARY KEY(account_id,id));

CREATE TABLE hosted_task_rehearsal_runs(account_id uuid NOT NULL,id text NOT NULL,run_id uuid NOT NULL,plan_sha256 text NOT NULL,reserved_at timestamptz NOT NULL,approval_expires_at timestamptz NOT NULL,PRIMARY KEY(account_id,id));
