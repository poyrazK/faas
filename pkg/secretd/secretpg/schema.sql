-- Projection of the tables secretd writes (ADR-20261110210000 §3–4),
-- from migration 20261111124927540.
CREATE TABLE secret_grants (
    app_id             uuid        NOT NULL,
    node_id            uuid        NOT NULL,
    granted_by_node_id uuid        NOT NULL,
    granted_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    PRIMARY KEY (app_id, node_id)
);

CREATE TABLE secret_open_audit (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id   uuid        NOT NULL,
    app_id    uuid,
    job_id    uuid,
    via       text        NOT NULL,
    entries   integer     NOT NULL,
    opened_at timestamptz NOT NULL DEFAULT now()
);

-- Projection of crash_captures (ADR-733), read by OpenCaptureKey: only the
-- columns secretd reads.
CREATE TABLE crash_captures (
    id         uuid  NOT NULL PRIMARY KEY,
    app_id     uuid  NOT NULL,
    status     text  NOT NULL,
    sealed_key bytea
);

-- Projection of snapshots, read by OpenCaptureKey for sealed park and warm
-- snapshots (ADR-20261110210000 slice 3d-3): only the columns secretd reads.
CREATE TABLE snapshots (
    id                uuid        NOT NULL PRIMARY KEY,
    deployment_id     uuid        NOT NULL,
    created_at        timestamptz NOT NULL,
    delete_pending    boolean     NOT NULL,
    sealed_key        bytea,
    sealed_capture_id uuid
);

-- Projection of app_secrets: the key and ciphertext OpenAppSecrets reads by
-- reference (ADR-20261110210000 slice 3d-4).
CREATE TABLE app_secrets (
    app_id     uuid  NOT NULL,
    scope      text  NOT NULL,
    key        text  NOT NULL,
    ciphertext bytea NOT NULL,
    PRIMARY KEY (app_id, scope, key)
);

-- Projection of app_log_drains: the sealed header OpenLogDrainHeader reads by
-- reference (ADR-20261110210000 slice 3f).
CREATE TABLE app_log_drains (
    id                 uuid  NOT NULL PRIMARY KEY,
    app_id             uuid  NOT NULL,
    auth_header_sealed bytea
);

-- Projection of triggers: the sealed config OpenTriggerConfig reads by
-- reference (ADR-20261110210000 slice 3g).
CREATE TABLE triggers (
    id     uuid  NOT NULL PRIMARY KEY,
    app_id uuid,
    kind   text  NOT NULL,
    config jsonb NOT NULL
);

-- Projections of edge_rule_header_secrets and edge_rules: the sealed value
-- OpenEdgeHeaderSecret reads by reference, and the rules that reference it
-- (ADR-20261110210000 slice 3i).
CREATE TABLE edge_rule_header_secrets (
    id         uuid  NOT NULL PRIMARY KEY,
    app_id     uuid  NOT NULL,
    ciphertext bytea NOT NULL
);

CREATE TABLE edge_rules (
    id     uuid  NOT NULL PRIMARY KEY,
    app_id uuid  NOT NULL,
    kind   text  NOT NULL,
    action jsonb NOT NULL
);

-- Projection of app_registry_credentials: the sealed password RegistryFetch
-- opens on the control plane (ADR-20261110210000 slice 3j).
CREATE TABLE app_registry_credentials (
    app_id             uuid  NOT NULL,
    registry           text  NOT NULL,
    username           text  NOT NULL,
    password_encrypted bytea NOT NULL
);

-- Projection of deployment_image_preparations: the node a deployment's image
-- preparation runs on, which may pull the app's private images
-- (ADR-20261110210000 slice 3j-2).
CREATE TABLE deployment_image_preparations (
    deployment_id uuid        NOT NULL PRIMARY KEY,
    node_name     text        NOT NULL,
    phase         text        NOT NULL,
    updated_at    timestamptz NOT NULL
);

-- Projection of deployments: which app a deployment belongs to
-- (ADR-20261110210000 slice 3j-2), and the registry repositories its builds
-- may pull (slice 3j-4).
CREATE TABLE deployments (
    id                   uuid NOT NULL PRIMARY KEY,
    app_id               uuid NOT NULL,
    build_registry_scope text[],
    status               text NOT NULL,
    scope                text NOT NULL
);

-- Projections of job_registry_credentials and jobs: a job's registry
-- credential and the node holding its materialization lease
-- (ADR-20261110210000 slice 3j-2b).
CREATE TABLE job_registry_credentials (
    job_id             uuid  NOT NULL,
    registry           text  NOT NULL,
    username           text  NOT NULL,
    password_encrypted bytea NOT NULL
);

CREATE TABLE jobs (
    id                                uuid NOT NULL PRIMARY KEY,
    image_materialization_lease_owner text,
    image_materialization_lease_until timestamptz
);

-- Projection of builds: the node running a build, recorded when it was
-- claimed (ADR-20261110210000 slice 3j-3).
CREATE TABLE builds (
    id                uuid NOT NULL PRIMARY KEY,
    deployment_id     uuid NOT NULL,
    status            text NOT NULL,
    started_at        timestamptz,
    builder_node_name text
);

CREATE TABLE managed_postgres_cutovers (
 id uuid PRIMARY KEY, account_id uuid NOT NULL, app_id uuid NOT NULL,
 target_database_id uuid NOT NULL, target_backend_id text NOT NULL,
 target_backend_fingerprint text NOT NULL, target_resource_id text NOT NULL,
 target_generation bigint NOT NULL, state text NOT NULL,
 lease_token text, lease_until timestamptz
);
CREATE TABLE managed_postgres_cutover_credentials (
 id uuid PRIMARY KEY, cutover_id uuid NOT NULL, environment_key text NOT NULL,
 access text NOT NULL, state text NOT NULL, provider_identity_id text,
 credential_ref text, ciphertext bytea, kid text, value_hash text
);
CREATE TABLE managed_postgres_databases (
 id uuid PRIMARY KEY, account_id uuid NOT NULL, state text NOT NULL,
 backend_id text NOT NULL, backend_fingerprint text NOT NULL,
 provider_resource_id text, desired_generation bigint NOT NULL, postgres_major smallint NOT NULL
);
CREATE TABLE secret_verification_audit (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 account_id uuid NOT NULL, app_id uuid NOT NULL, cutover_id uuid NOT NULL,
 credential_id uuid NOT NULL, verified_at timestamptz NOT NULL DEFAULT now()
);

-- Projections for storage grants (ADR-20261117190000 slice 2): where an
-- app's or a job's instances run, and which node runs an account's sandbox
-- sessions.
CREATE TABLE instances (
    id            uuid NOT NULL PRIMARY KEY,
    app_id        uuid,
    job_id        uuid,
    node_id       uuid NOT NULL,
    state         text NOT NULL
);

CREATE TABLE sandbox_sessions (
    id         uuid NOT NULL PRIMARY KEY,
    account_id uuid NOT NULL,
    node_id    text,
    placed_node_id text,
    state      text NOT NULL,
    template_id  uuid,
    image_job_id uuid
);

CREATE TABLE sandbox_templates (
    id     uuid NOT NULL PRIMARY KEY,
    app_id uuid NOT NULL
);

CREATE TABLE storage_grant_audit (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    node_id       uuid        NOT NULL,
    subject_class text        NOT NULL,
    subject_id    uuid        NOT NULL,
    app_id        uuid,
    access        text        NOT NULL,
    objects       integer     NOT NULL,
    granted_at    timestamptz NOT NULL DEFAULT now()
);

-- Projections of deployment_verification_leases and app_verification_contracts:
-- the node verifying a deployment and the contract whose header secret
-- references OpenVerificationSecrets opens (ADR-20261122220000 amendment 2).
CREATE TABLE deployment_verification_leases (
    deployment_id uuid        NOT NULL PRIMARY KEY,
    app_id        uuid        NOT NULL,
    node_name     text        NOT NULL,
    expires_at    timestamptz NOT NULL
);

CREATE TABLE app_verification_contracts (
    app_id   uuid  NOT NULL PRIMARY KEY,
    contract jsonb NOT NULL
);

-- Projection of mcp_listings: the probe secret the MCP tool contract gate
-- opens (ADR-20261127191645).
CREATE TABLE mcp_listings (
    app_id       uuid  NOT NULL PRIMARY KEY,
    contract     jsonb,
    probe_secret text  NOT NULL DEFAULT ''
);
