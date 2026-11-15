-- filename: 20261111193000292_app_basic_auth_verifiers.sql
-- migration-compat: expand
-- ADR-20261110210000 slice 3h: verify-only basic auth.
--
-- Any compute node's edge checks basic auth for any app, so the edge stops
-- opening the credential. apid, which has the plaintext when the customer
-- sets it, stores an argon2id verifier keyed by the SHA-256 of the sealed
-- blob in apps.public_auth_basic. Every path that copies the blob keeps the
-- key; a new credential is a new blob, so a verifier never outlives its
-- credential. Only apid writes: a compute login that could add a verifier
-- would choose another app's password on every node.

-- +goose Up
SET LOCAL lock_timeout = '5s';

CREATE TABLE IF NOT EXISTS app_basic_auth_verifiers (
    sealed_sha256 bytea PRIMARY KEY CHECK (octet_length(sealed_sha256) = 32),
    verifier text NOT NULL CHECK (verifier LIKE '$argon2id$%' AND length(verifier) <= 256),
    created_at timestamp with time zone NOT NULL DEFAULT now()
);

-- Default privileges give faas_runtime DML on new tables; runtime-roles.sql
-- repeats this and grants apid's own login what it needs.
-- +goose StatementBegin
DO $migration$
BEGIN
    IF to_regrole('faas_runtime') IS NOT NULL THEN
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON app_basic_auth_verifiers FROM faas_runtime;
    END IF;
    IF to_regrole('faas_apid') IS NOT NULL THEN
        GRANT SELECT, INSERT, DELETE ON app_basic_auth_verifiers TO faas_apid;
    END IF;
END;
$migration$;
-- +goose StatementEnd

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS app_basic_auth_verifiers;
