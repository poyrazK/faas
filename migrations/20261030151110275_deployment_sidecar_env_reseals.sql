-- filename: 20261030151110275_deployment_sidecar_env_reseals.sql
-- migration-compat: expand

-- +goose Up
SET LOCAL lock_timeout = '5s';
-- ADR 20261022111812: sidecar env values sit sealed inside
-- deployments.sidecars, which guard triggers keep immutable and which
-- readiness and promotion fingerprints hash. `gregalectl seal-domain migrate
-- --domain runtime` therefore writes a value's runtime-domain copy here
-- instead of rewriting the deployment. source_sha256 is the digest of the
-- declaration ciphertext the copy replaces: schedd sends the copy to vmmd
-- only while the deployment still declares exactly that ciphertext.
CREATE TABLE IF NOT EXISTS deployment_sidecar_env_reseals (
    deployment_id uuid NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    sidecar_name text NOT NULL CHECK (sidecar_name <> ''),
    env_key text NOT NULL CHECK (env_key <> ''),
    source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
    kid text NOT NULL CHECK (kid ~ '^age1[0-9a-z]{58}$'),
    ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) > 0),
    resealed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (deployment_id, sidecar_name, env_key)
);

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS deployment_sidecar_env_reseals;
