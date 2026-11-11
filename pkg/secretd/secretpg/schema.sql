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
    app_id    uuid        NOT NULL,
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
