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
-- (ADR-20261110210000 slice 3j-2).
CREATE TABLE deployments (
    id     uuid NOT NULL PRIMARY KEY,
    app_id uuid NOT NULL
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
