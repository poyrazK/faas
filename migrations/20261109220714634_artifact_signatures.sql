-- filename: 20261109220714634_artifact_signatures.sql
-- ADR-20261109170000 §3: signd records every artifact it signs, once per
-- storage key. A row is the control plane's word that the key was built by
-- that deployment on that node with that digest. Compute nodes log in as
-- faas_runtime members, so only faas_signd (signd's login) may insert; nobody
-- updates or deletes a row. The postgres role's runtime-roles.sql repeats the
-- grants on every converge, because faas_signd may not exist yet.
-- migration-compat: expand

-- +goose Up
-- +goose StatementBegin
CREATE TABLE artifact_signatures (
    storage_key      text        PRIMARY KEY CHECK (storage_key ~ '^apps/[^/]+/[^/]+\.ext4$' AND octet_length(storage_key) <= 512),
    digest           text        NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
    bytes            bigint      NOT NULL CHECK (bytes >= 0),
    deployment_id    uuid        NOT NULL,
    node_name        text        NOT NULL CHECK (node_name <> '' AND octet_length(node_name) <= 253),
    signature_format text        NOT NULL CHECK (signature_format IN ('v2')),
    signed_at        timestamptz NOT NULL DEFAULT now() CHECK (isfinite(signed_at))
);
CREATE INDEX artifact_signatures_deployment ON artifact_signatures (deployment_id);

DO $$
BEGIN
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE INSERT, UPDATE, DELETE ON artifact_signatures FROM faas_runtime;
    END IF;
    IF to_regrole('faas_signd') IS NOT NULL THEN
        GRANT SELECT, INSERT ON artifact_signatures TO faas_signd;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE artifact_signatures;
-- +goose StatementEnd
