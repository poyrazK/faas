-- Projection of the tables signd reads and writes (ADR-20261109170000 §3).
-- artifact_signatures: migration 20261109220714634. The others are the
-- columns signd reads, from the deployments, apps and image preparation
-- migrations.
CREATE TABLE artifact_signatures (
    storage_key      text        PRIMARY KEY,
    digest           text        NOT NULL,
    bytes            bigint      NOT NULL,
    deployment_id    uuid        NOT NULL,
    node_name        text        NOT NULL,
    signature_format text        NOT NULL,
    signed_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE apps (
    id   uuid PRIMARY KEY,
    slug text NOT NULL
);

CREATE TABLE deployments (
    id       uuid  PRIMARY KEY,
    app_id   uuid  NOT NULL,
    status     text  NOT NULL,
    sidecars   jsonb,
    deleted_at timestamptz
);

CREATE TABLE deployment_image_preparations (
    deployment_id uuid PRIMARY KEY,
    node_name     text NOT NULL,
    phase         text NOT NULL
);
